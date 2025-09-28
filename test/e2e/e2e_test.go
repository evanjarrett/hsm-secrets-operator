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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/evanjarrett/hsm-secrets-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "hsm-secrets-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "hsm-secrets-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "hsm-secrets-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "hsm-secrets-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		// Set up cleanup that runs even if BeforeAll fails partway through
		DeferCleanup(func() {
			By("cleaning up resources from BeforeAll setup")

			By("undeploying the controller-manager")
			cmd := exec.Command("make", "undeploy")
			_, _ = utils.Run(cmd)

			By("uninstalling CRDs")
			cmd = exec.Command("make", "uninstall")
			_, _ = utils.Run(cmd)

			By("removing manager namespace")
			cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true")
			_, _ = utils.Run(cmd)
		})

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("waiting for deployment to be ready")
		cmd = exec.Command("kubectl", "rollout", "status",
			"deployment/hsm-secrets-operator-controller-manager", "-n", namespace, "--timeout=60s")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to wait for manager deployment to be ready")

		By("deploying test HSM devices to trigger discovery and agent deployment")
		cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/test-hsm-device.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy test HSM device")
	})

	// Note: Main cleanup is handled by DeferCleanup in BeforeAll

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging, then clean up test-specific resources.
	AfterEach(func() {
		// Clean up test-specific resources regardless of success/failure
		DeferCleanup(func() {
			By("cleaning up the curl pod for metrics")
			cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found=true")
			_, _ = utils.Run(cmd)

			By("cleaning up the metrics ClusterRoleBinding")
			cmd = exec.Command("kubectl", "delete", "clusterrolebinding", metricsRoleBindingName, "--ignore-not-found=true")
			_, _ = utils.Run(cmd)
		})

		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			if controllerPodName != "" {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				controllerLogs, err := utils.Run(cmd)
				if err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
				} else {
					_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
				}

				By("Fetching controller manager pod description")
				cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
				podDescription, err := utils.Run(cmd)
				if err == nil {
					fmt.Println("Pod description:\n", podDescription)
				} else {
					fmt.Println("Failed to describe controller pod")
				}
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller pod name not available for logs\n")

				By("Fetching all pods in namespace for debugging")
				cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide")
				podsOutput, err := utils.Run(cmd)
				if err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "All pods in namespace:\n%s", podsOutput)
				}
			}

			By("Fetching Kubernetes events")
			cmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil && metricsOutput != "" {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else if err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("ensuring ClusterRoleBinding for the service account to allow access to metrics")
			// First check if it already exists
			cmd := exec.Command("kubectl", "get", "clusterrolebinding", metricsRoleBindingName)
			_, err := utils.Run(cmd)
			if err != nil {
				// ClusterRoleBinding doesn't exist, create it
				cmd = exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
					"--clusterrole=hsm-secrets-operator-metrics-reader",
					fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
				)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")
			}

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("\"controller-runtime.metrics\",\"msg\":\"Starting metrics server\""),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccount": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			// Check for metrics that should always be present
			Expect(metricsOutput).To(ContainSubstring("go_goroutines"))
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		It("should have API server start after cache is ready", func() {
			By("verifying that the API server starts properly after manager cache")
			verifyAPIServerStartup := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// The API server should start after the manager cache is ready
				// Look for manager startup and API server startup in the correct order
				g.Expect(output).To(ContainSubstring("Starting API server"),
					"API server should have started")

				// Should not see the cache error that indicates race condition
				g.Expect(output).NotTo(ContainSubstring("the cache is not started, can not read objects"),
					"API server should not attempt to read from cache before it's started")
			}
			Eventually(verifyAPIServerStartup).Should(Succeed())
		})

		It("should serve API requests without 'no_agents' error immediately after startup", func() {
			By("creating a test pod to call the API server health endpoint")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			// Create a pod that will test the API server
			apiURL := fmt.Sprintf("http://hsm-secrets-operator-hsm-secrets-operator-api.%s.svc.cluster.local:8090/api/v1/health",
				namespace)
			curlCmd := fmt.Sprintf("curl -v -H 'Authorization: Bearer %s' %s", token, apiURL)
			overrides := fmt.Sprintf(`{"spec":{"containers":[{"name":"curl","image":"curlimages/curl:latest",`+
				`"command":["/bin/sh","-c"],"args":["%s"],`+
				`"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},`+
				`"runAsNonRoot":true,"runAsUser":1000,"seccompProfile":{"type":"RuntimeDefault"}}}],`+
				`"serviceAccount":"%s"}}`, curlCmd, serviceAccountName)
			cmd := exec.Command("kubectl", "run", "api-test", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides", overrides)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create api-test pod")

			// Clean up the test pod
			DeferCleanup(func() {
				cmd := exec.Command("kubectl", "delete", "pod", "api-test", "-n", namespace, "--ignore-not-found=true")
				_, _ = utils.Run(cmd)
			})

			By("waiting for the api-test pod to complete")
			verifyAPITestComplete := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "api-test",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "api-test pod should complete successfully")
			}
			Eventually(verifyAPITestComplete, 2*time.Minute).Should(Succeed())

			By("checking the API response in the test pod logs")
			cmd = exec.Command("kubectl", "logs", "api-test", "-n", namespace)
			apiOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from api-test pod")

			// Should get a successful response, not a 'no_agents' error
			Expect(apiOutput).To(ContainSubstring("< HTTP/1.1 200 OK"),
				"API health endpoint should return 200 OK")
			Expect(apiOutput).To(ContainSubstring("\"success\":true"),
				"API should return successful response")
			Expect(apiOutput).NotTo(ContainSubstring("no_agents"),
				"API should not return 'no_agents' error after cache is ready")
		})

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput := getMetricsOutput()
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
