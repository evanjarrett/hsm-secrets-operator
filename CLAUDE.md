# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## ⚠️ Important Development Context

**Remote Kubernetes Environment**: The Kubernetes cluster is running remotely, NOT on this local development system. Any local device checks (like `ls /dev/tty*` or local USB device detection) will NOT work and will not reflect the actual state of devices on the remote cluster nodes.

**Sync Architecture**: The operator implements **unidirectional sync from HSM to Kubernetes Secrets only**. HSM is the authoritative source of truth. K8s Secrets are read-only replicas that get updated when HSM data changes. There is no K8s → HSM sync functionality.

**GitOps Deployment**: This Kubernetes deployment is GitOps-based. You are not able to push new images to the Kubernetes cluster from this machine. Code changes require updating the deployment through the GitOps pipeline.

**Code Modernization**: Avoid keeping legacy code whenever possible. Replace and improve functions as necessary rather than maintaining backward compatibility with outdated patterns.

## Project Overview

A Kubernetes operator that bridges Hardware Security Module (HSM) data storage with Kubernetes Secrets, providing true secret portability through hardware-based security. The operator implements a controller pattern that synchronizes HSM binary data files to Kubernetes Secret objects using a unified binary architecture with gRPC communication, automatic USB device discovery, and dynamic agent deployment.

## Architecture: Unified Binary with Mode-Based Operation

The project uses a **unified binary** (`cmd/hsm-operator/main.go`) that operates in different modes, replacing the previous four-binary architecture. A separate test utility (`cmd/test-hsm/main.go`) provides HSM testing capabilities.

### Core Components

**Unified Binary Modes:**
- **Manager Mode** (`--mode=manager`): 
  - Orchestrates HSMSecret resources and deploys agents
  - Provides unified REST API proxy on port 8090
  - Handles HSMPool aggregation from discovery pod annotations
  - Controllers: `HSMSecretReconciler`, `HSMPoolReconciler`, `HSMPoolAgentReconciler`, `DiscoveryDaemonSetReconciler`

- **Discovery Mode** (`--mode=discovery`):
  - DaemonSet that discovers USB HSM devices on cluster nodes
  - Reports findings via pod annotations (race-free architecture)
  - Native sysfs scanning with Talos Linux support
  
- **Agent Mode** (`--mode=agent`):
  - Dynamically deployed pods for direct HSM communication
  - gRPC server on port 9090 with HTTP health checks on port 8093
  - Real PKCS#11 client for production or MockClient for testing

**Test Utility:**
- **Test HSM Binary** (`cmd/test-hsm/main.go`): Standalone HSM operations testing and debugging

### Key Architectural Patterns

**Race-Free Coordination:**
- HSMDevice CRDs contain readonly specifications only (no status field)
- Discovery pods report via their own pod annotations 
- **HSMPool CRDs are the source of truth** for agent discovery and multi-device operations
- HSMPool aggregates all discovery reports from multiple nodes
- Owner references ensure automatic cleanup when resources are deleted
- 5-minute grace periods prevent agent churn during outages

**Multi-Device Agent Architecture:**
- **HSMPool-based Agent Discovery**: API and controllers query HSMPool to find all agent instances for a device type
- **Multiple Agent Instances**: Each physical device gets its own agent pod (e.g., `hsm-agent-pico-hsm-0`, `hsm-agent-pico-hsm-1`)
- **Multi-Agent Operations**: API operations (list, write, delete) work across all agents when mirroring is enabled
- **Automatic Synchronization**: HSMMirrorReconciler handles conflict detection and resolution between devices

**gRPC Communication Architecture:**
- Protocol definition in `api/proto/hsm/v1/hsm.proto` with 10 HSM operations
- Manager ↔ Agent: gRPC for efficient, type-safe HSM operations  
- Discovery → Manager: Pod annotations for race-free device reporting
- **External → Manager**: REST API proxy routes to ALL agents for multi-device operations
- Generated code: `api/proto/hsm/v1/hsm.pb.go` and `hsm_grpc.pb.go`

**Controller Hierarchy:**
```
Manager Controllers:
├── HSMSecretReconciler - HSM to K8s Secret sync
├── HSMPoolReconciler - Aggregates discovery reports from pod annotations  
├── HSMPoolAgentReconciler - Deploys agents when pools are ready
├── HSMMirrorReconciler - Multi-device HSM mirroring and conflict resolution
└── DiscoveryDaemonSetReconciler - Manages discovery DaemonSet lifecycle

Discovery Controllers:
└── HSMDeviceController - USB device discovery via sysfs scanning
```

## Essential Development Commands

### Quality Checks (REQUIRED before committing)
```bash
# Single command for all quality checks (format + vet + lint)  
make quality

# Run tests
make test

# Run specific test packages
go test ./internal/controller -v -run TestHSMSecretController
go test ./internal/hsm -v
go test ./internal/discovery -v
```

### Build and Run
```bash
# Build unified binary
make build  # Creates bin/hsm-operator

# Run in different modes
make run                    # Manager mode (default)  
make run-agent             # Agent mode (requires HSM device)
make run-discovery         # Discovery mode

# Build test utility
go build -o bin/test-hsm cmd/test-hsm/main.go

# Test specific modes
./bin/hsm-operator --mode=manager --help
./bin/hsm-operator --mode=agent --device-name=pico-hsm --help  
./bin/hsm-operator --mode=discovery --node-name=worker1 --help
```

### Protocol Buffer Development
```bash
# After modifying api/proto/hsm/v1/hsm.proto
buf generate                # Generate Go code from .proto files
buf lint                   # Lint protobuf files  
buf format -w              # Format protobuf files

# Verify changes don't break existing code
make test
```

### CRD Development
```bash
# After modifying api/v1alpha1/*.go files
make manifests generate    # Generate CRDs and DeepCopy methods
make helm-sync            # Sync CRDs from config/ to helm/
make quality test         # Verify changes
```

### Docker & Deployment
```bash
# Production image (agent has PKCS#11 support)
make docker-build IMG=hsm-secrets-operator:latest

# Testing image (mock clients only, no CGO dependencies)  
make docker-build-testing IMG=hsm-secrets-operator:latest

# Deploy to cluster
make deploy IMG=hsm-secrets-operator:latest

# Generate installer bundle  
make build-installer IMG=hsm-secrets-operator:latest
```

## CRD Structure and Relationships

### HSMSecret CRD (Secret Management)
```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: my-secret                   # HSM path = metadata.name
spec:
  autoSync: true                    # Automatic sync from HSM to K8s (default)
  syncInterval: 30                  # Sync interval in seconds  
status:
  syncStatus: "InSync"              # InSync|OutOfSync|Error|Pending
  hsmChecksum: "sha256:abc123..."   # SHA256 checksum for change detection
  secretChecksum: "sha256:def456..."
```

### HSMDevice CRD (Device Specifications) 
```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: my-pico-hsm
spec:
  deviceType: "PicoHSM"
  discovery:
    usb:
      vendorId: "20a0" 
      productId: "4230"
    # OR autoDiscovery: true for well-known device types
  pkcs11:
    libraryPath: "/usr/lib/opensc-pkcs11.so"  # Use OpenSC for Pico HSM
    slotId: 0
    pinSecret:
      name: "hsm-pin"
      key: "pin"
# NOTE: No status field - readonly specs only, discovery via pod annotations
```

### HSMPool CRD (Device Aggregation)
```yaml
apiVersion: hsm.j5t.io/v1alpha1  
kind: HSMPool
status:
  phase: "Ready"                    # Pending|Aggregating|Ready|Partial|Error
  totalDevices: 2
  reportingPods:                    # Discovery pods currently reporting
    - podName: "discovery-node1"
      devicesFound: 1
      fresh: true                   # Within grace period
  aggregatedDevices:                # All discovered devices across cluster  
    - devicePath: "/dev/bus/usb/001/015"
      nodeName: "worker-1"
      serialNumber: "DC6A33145E23A42A" 
      available: true
```

## PKCS#11 Library Requirements

**⚠️ Critical: Use OpenSC Library for Pico HSM**

The Pico HSM requires the **OpenSC PKCS#11 library** (`/usr/lib/opensc-pkcs11.so`) instead of the CardContact library for proper data object support.

**Build Architecture:**
- **Production Build** (`Dockerfile`): Agent has CGO enabled with real PKCS#11Client
- **Conditional Compilation**: `internal/hsm/pkcs11_client.go` vs `pkcs11_client_nocgo.go` based on build tags

## Common Development Workflows

### Adding New gRPC Operation
```bash
# 1. Add new RPC method to api/proto/hsm/v1/hsm.proto
# 2. Add request/response message types
# 3. Generate code
buf generate
# 4. Implement in internal/agent/grpc_server.go  
# 5. Update client in internal/agent/grpc_client.go
# 6. Test changes
make test
```

### Testing with Real HSM Device
```bash
# Build test utility
go build -o bin/test-hsm cmd/test-hsm/main.go

# Test full cycle (write/read/verify/delete)
./bin/test-hsm --library=/usr/lib/opensc-pkcs11.so --pin=$PIN --op=test

# Test specific operations
./bin/test-hsm --library=/usr/lib/opensc-pkcs11.so --pin=$PIN --op=list
./bin/test-hsm --library=/usr/lib/opensc-pkcs11.so --pin=$PIN --op=write --path=test-secret
```

### API Testing 
```bash
# Port forward to access REST API
kubectl port-forward -n hsm-secrets-operator-system svc/hsm-secrets-operator-api 8090:8090

# Test API endpoints
cd examples/api
./create-secret.sh my-test-secret
./list-secrets.sh

# Direct curl
curl http://localhost:8090/api/v1/hsm/secrets/my-test-secret | jq '.'
```

## Monitoring and Troubleshooting

### Resource Status Monitoring
```bash
# View resources with custom columns
kubectl get hsmsecret                   # Short name: hsmsec
kubectl get hsmdevice                   # Short name: hsmdev  
kubectl get hsmpool

# Monitor sync status
kubectl get hsmsecret my-secret -o jsonpath='{.status.syncStatus}'

# Check discovered devices in HSMPool (source of truth for agents)
kubectl get hsmpool -o jsonpath='{.status.aggregatedDevices[*].devicePath}'

# Check HSMPool readiness for agent deployment
kubectl get hsmpool -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,DEVICES:.status.totalDevices

# View all agent pods for multi-device setup
kubectl get pods -l app.kubernetes.io/name=hsm-agent

# View discovery pod reports  
kubectl get pods -l app.kubernetes.io/component=discovery \
  -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.annotations.hsm\.j5t\.io/device-report}{"\n"}{end}'
```

### Port Configuration
- **Manager API Server**: Port 8090 (REST API)
- **Manager Metrics**: Port 8080 internal, 8443 via service
- **Manager Health**: Port 8081  
- **Agent gRPC**: Port 9090 (HSM operations)
- **Agent Health**: Port 8093 (HTTP health checks)

### Common Issues
- **API works, pkcs11-tool doesn't see objects**: Use `--login --pin` for private objects
- **`CKR_DEVICE_REMOVED` errors**: Restart agent pod to reset PKCS#11 session
- **`CKR_TEMPLATE_INCONSISTENT` errors**: Switch from CardContact to OpenSC library  
- **Agent crash loop**: Check library path and PIN secret configuration
- **gRPC connection failed**: Verify agent on port 9090, check service/endpoint configuration
- **Proto generation issues**: Install buf tool (`go install github.com/bufbuild/buf/cmd/buf@latest`)
- **Metadata keys in K8s Secrets**: If you see `_metadata` keys in Kubernetes Secrets, this indicates a bug in the PKCS#11 client filtering (should be excluded in `ReadSecret`)

## Manual HSM Access

```bash
# Get agent pod
AGENT_POD=$(kubectl get pods -l app.kubernetes.io/name=hsm-agent -o jsonpath='{.items[0].metadata.name}')

# List all secrets (requires PIN authentication) 
kubectl exec $AGENT_POD -- pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin="$PKCS11_PIN" --list-objects --type=data

# Read specific secret component
kubectl exec $AGENT_POD -- pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin="$PKCS11_PIN" --read-object --type=data --label="my-secret/api_key"

# HSM device info
kubectl exec $AGENT_POD -- pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" -I
```

**Secret Storage Structure:**
- Each K8s Secret becomes multiple PKCS#11 data objects
- Object naming: `secret-name/key-name` (e.g., `user-credentials/api_key`)
- Metadata stored separately with `/_metadata` suffix (filtered out from K8s Secrets)
- Private objects require PIN authentication to access

## Code Architecture Critical Points

**Controller Interaction Flow:**
1. `HSMSecretReconciler` reads from HSM via gRPC agents
2. `HSMPoolReconciler` aggregates device discovery reports from pod annotations (race-free)
3. `HSMPoolAgentReconciler` deploys agents dynamically when devices are ready
4. `HSMSyncReconciler` handles multi-device HSM synchronization (HSM ↔ HSM only)

**Agent Discovery Architecture:**
- **HSMPool as Source of Truth**: API and controllers query HSMPool.Status.AggregatedDevices instead of individual HSMDevice resources
- **Multi-Instance Agent Tracking**: Agent manager tracks agents by keys like `pico-hsm-0`, `pico-hsm-1` for multiple physical devices
- **Pool-Based Cleanup**: Agent cleanup based on HSMSecret existence rather than device-specific references
- **API Multi-Device Operations**: 
  - `findAvailableAgent()` queries HSMPools to find any available agent for a device type
  - `getAllAvailableAgents()` returns all agents across all pools for mirroring operations
  - Operations like delete/write with `mirror=true` target ALL agents simultaneously

**PKCS#11 Client Implementation:**
- Production: `internal/hsm/pkcs11_client.go` with CGO
- Testing: `internal/hsm/pkcs11_client_nocgo.go` and `mock_client.go`
- **Critical**: ReadSecret must filter out `metadataKeySuffix` ("/_metadata") objects
- Build tags control which client is compiled

**Protocol Buffer Workflow:**
1. Modify `api/proto/hsm/v1/hsm.proto`
2. Run `buf generate` to update Go code
3. Implement in `internal/agent/grpc_server.go`
4. Update client calls in controller or agent code

This operator provides secure, hardware-backed secret management that integrates seamlessly with Kubernetes while maintaining the security benefits of HSM-based storage.