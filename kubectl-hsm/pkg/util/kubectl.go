/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	operatorServiceName = "hsm-secrets-operator-api"
	operatorServicePort = 8090
)

// KubectlUtil provides kubectl integration utilities
type KubectlUtil struct {
	config    *rest.Config
	clientset *kubernetes.Clientset
	namespace string
}

// NewKubectlUtil creates a new kubectl utility instance
func NewKubectlUtil(namespace string) (*KubectlUtil, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	// Use provided namespace or get current namespace from kubeconfig
	if namespace == "" {
		namespace, err = getCurrentNamespace()
		if err != nil {
			return nil, fmt.Errorf("failed to get current namespace: %w", err)
		}
	}

	return &KubectlUtil{
		config:    config,
		clientset: clientset,
		namespace: namespace,
	}, nil
}

// GetCurrentNamespace returns the current namespace from kubeconfig
func (k *KubectlUtil) GetCurrentNamespace() string {
	return k.namespace
}

// FindOperatorService finds the HSM operator service in the current namespace
func (k *KubectlUtil) FindOperatorService(ctx context.Context) error {
	svc, err := k.clientset.CoreV1().Services(k.namespace).Get(ctx, operatorServiceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("HSM secrets operator service not found in namespace '%s': %w\n\nPlease check:\n  - Is the operator installed? Try: kubectl get deploy -n %s\n  - Are you in the correct namespace? Try: kubens <operator-namespace>",
			k.namespace, err, k.namespace)
	}

	// Check if service has the expected port
	found := false
	for _, port := range svc.Spec.Ports {
		if port.Port == operatorServicePort {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("operator service '%s' does not expose port %d", operatorServiceName, operatorServicePort)
	}

	return nil
}

// CreatePortForward creates a port forward to the operator service
func (k *KubectlUtil) CreatePortForward(ctx context.Context, localPort int, verbose bool) (*PortForward, error) {
	// First check if the service exists
	if err := k.FindOperatorService(ctx); err != nil {
		return nil, err
	}

	// Get a pod from the operator deployment
	pods, err := k.clientset.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=hsm-secrets-operator,control-plane=controller-manager",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list operator pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no operator manager pods found in namespace '%s'", k.namespace)
	}

	pod := pods.Items[0]
	if pod.Status.Phase != "Running" {
		return nil, fmt.Errorf("operator pod '%s' is not running (status: %s)", pod.Name, pod.Status.Phase)
	}

	// Create port forward
	pf := &PortForward{
		config:     k.config,
		clientset:  k.clientset,
		namespace:  k.namespace,
		podName:    pod.Name,
		localPort:  localPort,
		remotePort: operatorServicePort,
		stopCh:     make(chan struct{}),
		readyCh:    make(chan struct{}),
		verbose:    verbose,
	}

	if err := pf.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start port forward: %w", err)
	}

	return pf, nil
}

// PortForward manages a port forward connection
type PortForward struct {
	config     *rest.Config
	clientset  *kubernetes.Clientset
	namespace  string
	podName    string
	localPort  int
	remotePort int
	stopCh     chan struct{}
	readyCh    chan struct{}
	verbose    bool
}

// Start starts the port forward
func (pf *PortForward) Start(ctx context.Context) error {
	req := pf.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(pf.namespace).
		Name(pf.podName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(pf.config)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	ports := []string{fmt.Sprintf("%d:%d", pf.localPort, pf.remotePort)}

	// Control output based on verbose flag
	var stdout, stderr io.Writer
	if pf.verbose {
		stdout = os.Stdout
		stderr = os.Stderr
	} else {
		stdout = io.Discard
		stderr = io.Discard
	}

	forwarder, err := portforward.New(dialer, ports, pf.stopCh, pf.readyCh, stdout, stderr)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	go func() {
		if err := forwarder.ForwardPorts(); err != nil && pf.verbose {
			fmt.Fprintf(os.Stderr, "Port forward error: %v\n", err)
		}
	}()

	// Wait for port forward to be ready with timeout
	select {
	case <-pf.readyCh:
		return nil
	case <-time.After(10 * time.Second):
		pf.Stop()
		return fmt.Errorf("port forward did not become ready within 10 seconds")
	case <-ctx.Done():
		pf.Stop()
		return ctx.Err()
	}
}

// Stop stops the port forward
func (pf *PortForward) Stop() {
	close(pf.stopCh)
}

// GetLocalPort returns the local port being forwarded
func (pf *PortForward) GetLocalPort() int {
	return pf.localPort
}

// getKubeConfig gets the Kubernetes client configuration
func getKubeConfig() (*rest.Config, error) {
	// Try in-cluster config first (for when running in pod)
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	// Fall back to kubeconfig file
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig from %s: %w", kubeconfig, err)
	}

	return config, nil
}

// getCurrentNamespace gets the current namespace from kubeconfig
func getCurrentNamespace() (string, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}

	configLoader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{},
	)

	namespace, _, err := configLoader.Namespace()
	if err != nil {
		return "", fmt.Errorf("failed to get namespace from kubeconfig: %w", err)
	}

	if namespace == "" {
		namespace = "default"
	}

	return namespace, nil
}
