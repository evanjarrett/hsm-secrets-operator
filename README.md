# hsm-secrets-operator

A Kubernetes operator that bridges Hardware Security Module (HSM) data storage with Kubernetes Secrets, providing true secret portability through hardware-based security.

## Description

The HSM Secrets Operator implements a controller pattern that maintains bidirectional synchronization between HSM binary data files and Kubernetes Secret objects. It uses a four-binary architecture with gRPC communication, automatic USB device discovery, and dynamic agent deployment to provide secure, hardware-backed secret management in Kubernetes environments.

### Key Features

- **Hardware Security**: Leverages Pico HSM and other PKCS#11 compatible devices for tamper-resistant secret storage
- **Bidirectional Sync**: Automatic synchronization between HSM storage and Kubernetes Secrets
- **Device Discovery**: Automatic USB HSM device detection with support for multiple device types
- **Agent Architecture**: Dynamic deployment of HSM agent pods with node affinity for direct hardware access
- **gRPC Communication**: High-performance gRPC protocol for manager-agent communication with fallback to HTTP
- **Unified API**: Single REST API endpoint that routes operations to appropriate HSM agents
- **Secret Portability**: Move secrets between clusters by carrying the HSM device
- **Multi-Device Support**: Support for Pico HSM, SmartCard-HSM, YubiKey HSM, and custom devices

### Architecture

The operator consists of four main components:

1. **Manager**: Orchestrates HSMSecret resources, deploys agents, and provides unified REST API proxy (port 8090)
2. **Discovery**: DaemonSet that discovers USB HSM devices on cluster nodes and reports via pod annotations
3. **Agent**: Dynamically deployed pods that handle direct HSM communication via gRPC (port 9090) with HTTP health checks (port 8093)
4. **Test HSM**: Utility for HSM operations testing and debugging

**Communication Architecture:**
- **Manager ↔ Agent**: gRPC for efficient, type-safe HSM operations
- **Discovery → Manager**: Pod annotations for race-free device reporting  
- **External → Manager**: REST API for user/application access
- **Protocol Buffers**: Structured message definitions in `api/proto/hsm/v1/hsm.proto`

This architecture ensures that HSM operations only occur on nodes with physical device access while providing a centralized management interface with high-performance communication.

## Getting Started

### Prerequisites
- Kubernetes v1.20+ cluster
- Go 1.24+ (for building from source)
- Docker 17.03+ (for building images)
- kubectl with cluster-admin privileges
- HSM device (Pico HSM, SmartCard-HSM, YubiKey HSM, or compatible PKCS#11 device)
- **For development**: buf tool (`go install github.com/bufbuild/buf/cmd/buf@latest`)

### Deployment Options

#### Option 1: Using Helm (Recommended)

1. **Deploy with Helm:**
```bash
# Install from local chart
helm install hsm-secrets-operator helm/hsm-secrets-operator \
  --namespace hsm-secrets-operator-system \
  --create-namespace
```

2. **Configure HSM Devices:**
```bash
# Apply HSMDevice configuration for your HSM type
kubectl apply -f config/samples/hsm_v1alpha1_hsmdevice.yaml
```

3. **Create HSM Secrets:**
```bash
# Create HSMSecret resources to sync with Kubernetes Secrets
kubectl apply -f config/samples/hsm_v1alpha1_hsmsecret.yaml
```

#### Option 2: Manual Deployment

1. **Build and deploy images:**
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/hsm-secrets-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/hsm-secrets-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### Quick Start with API

Once deployed, the operator provides a unified API endpoint:

```bash
# Port forward to access the API
kubectl port-forward -n hsm-secrets-operator-system svc/hsm-secrets-operator-api 8090:8090

# Check API health
curl http://localhost:8090/api/v1/health

# List secrets
curl http://localhost:8090/api/v1/hsm/secrets

# Check discovered HSM devices  
kubectl get hsmdevices

# Check HSM pools (aggregated device discovery)
kubectl get hsmpools

# Check agent pods (deployed automatically when devices are ready)
kubectl get pods -l app.kubernetes.io/component=agent

# Test gRPC agent health (from within cluster)
kubectl exec -it <agent-pod> -- curl http://localhost:8093/healthz
```

### Uninstallation

#### Using Helm:
```bash
helm uninstall hsm-secrets-operator -n hsm-secrets-operator-system
```

#### Manual cleanup:
```bash
# Delete sample resources
kubectl delete -k config/samples/

# Delete operator
make undeploy

# Delete CRDs (optional - will remove all HSMSecret/HSMDevice resources)
make uninstall
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/hsm-secrets-operator:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/hsm-secrets-operator/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
operator-sdk edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing

We welcome contributions! The HSM Secrets Operator follows standard Kubernetes operator development practices.

### Development Setup

1. **Clone and setup:**
```bash
git clone <repository-url>
cd hsm-secrets-operator
```

2. **Run quality checks:**
```bash
# Format, vet, and lint code (REQUIRED before committing)
make quality

# Run tests
make test
```

3. **Local development:**
```bash
# Generate manifests after CRD changes
make manifests

# Generate protocol buffer code after .proto changes
buf generate

# Build all binaries (manager, discovery, agent, test-hsm)
make build
```

### Code Quality Requirements

**⚠️ CRITICAL: Always run before committing:**
```bash
make quality  # Runs format + vet + lint
```

### Architecture Notes

- **Manager**: Handles HSMSecret CRDs, agent deployment, and REST API proxy (port 8090)
- **Discovery**: DaemonSet for USB device discovery with pod annotation reporting
- **Agent**: Dynamic pods for direct HSM communication via gRPC (port 9090)
- **gRPC Protocol**: Type-safe communication defined in `api/proto/hsm/v1/hsm.proto`
- **Health Checks**: HTTP endpoints on port 8093 for Kubernetes probes

### Protocol Buffer Development

When modifying the gRPC service definition:

```bash
# 1. Edit the protocol definition
vim api/proto/hsm/v1/hsm.proto

# 2. Generate Go code  
buf generate

# 3. Lint and format
buf lint
buf format -w api/proto/hsm/v1/hsm.proto

# 4. Run tests to ensure compatibility
make test
```

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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
