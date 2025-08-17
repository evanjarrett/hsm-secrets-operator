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

package main

import (
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
	"github.com/evanjarrett/hsm-secrets-operator/internal/controller"
	"github.com/evanjarrett/hsm-secrets-operator/internal/discovery"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("discovery")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hsmv1alpha1.AddToScheme(scheme))
}

func main() {
	var nodeName string
	var syncInterval time.Duration
	flag.StringVar(&nodeName, "node-name", "", "The name of the node this discovery agent is running on")
	flag.DurationVar(&syncInterval, "sync-interval", 30*time.Second, "Interval for device discovery sync")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if nodeName == "" {
		if name := os.Getenv("NODE_NAME"); name != "" {
			nodeName = name
		} else if hostname, err := os.Hostname(); err == nil {
			nodeName = hostname
		} else {
			setupLog.Error(nil, "node name must be provided via --node-name flag or NODE_NAME environment variable")
			os.Exit(1)
		}
	}

	setupLog.Info("Starting HSM device discovery agent", "node", nodeName, "sync-interval", syncInterval)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false, // No leader election needed for discovery
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Initialize USB discoverer
	usbDiscoverer := discovery.NewUSBDiscoverer()

	// Initialize mirroring manager for cross-node HSM device synchronization
	mirroringManager := discovery.NewMirroringManager(mgr.GetClient(), nodeName)

	if err := (&controller.HSMDeviceReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		NodeName:         nodeName,
		USBDiscoverer:    usbDiscoverer,
		MirroringManager: mirroringManager,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HSMDevice")
		os.Exit(1)
	}

	setupLog.Info("starting device discovery manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
