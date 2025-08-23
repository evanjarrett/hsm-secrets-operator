# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## ⚠️ Important Development Context

**Remote Kubernetes Environment**: The Kubernetes cluster is running remotely, NOT on this local development system. Any local device checks (like `ls /dev/tty*` or local USB device detection) will NOT work and will not reflect the actual state of devices on the remote cluster nodes.

## Development Commands

### Building and Testing
```bash
# Build all binaries (manager, discovery, agent)
make build                    # Builds bin/manager, bin/discovery, bin/agent

# Build specific components individually
go build -o bin/manager cmd/manager/main.go
go build -o bin/discovery cmd/discovery/main.go  
go build -o bin/agent cmd/agent/main.go
go build -o bin/test-hsm cmd/test-hsm/main.go

# Run local development
make run                      # Run manager locally (requires cluster access)

# Testing
make test                     # Run unit tests with coverage
make test-e2e                 # Run end-to-end tests (requires Kind cluster) 
make setup-test-e2e          # Set up Kind cluster for e2e testing
make cleanup-test-e2e        # Tear down Kind cluster

# NOTE: E2E tests are slow and run manually or nightly (not on every push)
# To trigger E2E tests manually in GitHub Actions:
# Go to Actions tab -> "E2E Tests" -> "Run workflow"

# Run specific test packages
go test ./internal/controller -v
go test ./internal/hsm -v
go test ./internal/discovery -v
go test ./internal/api -v

# Code quality (ALWAYS RUN BEFORE COMMITTING)
make fmt                      # Format code (runs go fmt ./...)
make vet                      # Run go vet
make lint                     # Run golangci-lint ./... (configured via .golangci.yml)
make lint-fix                 # Run golangci-lint with auto-fixes
make lint-config             # Verify golangci-lint configuration
make quality                  # Run all quality checks (fmt + vet + lint)

# Quality check workflow for development  
make quality                  # ONE COMMAND: Format + vet + lint (RECOMMENDED)
# OR run individually:
gofmt -w .                    # Format all Go files
golangci-lint run ./...       # Lint all packages (REQUIRED before code changes)

# CRD and manifest generation
make manifests               # Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects
make generate                # Generate DeepCopy methods for CRD types
make helm-sync               # Sync generated CRDs from config/ to helm/ after CRD changes

# Protocol Buffer generation (for gRPC)
buf generate                 # Generate Go code from proto files (requires buf tool)
buf lint                     # Lint protobuf files
buf format -w                # Format protobuf files
```

### Docker Images
```bash
# Production image (agent has PKCS#11 support, manager/discovery use mock clients)
make docker-build IMG=hsm-secrets-operator:latest

# Testing image (all binaries without CGO, uses mock clients only)
make docker-build-testing IMG=hsm-secrets-operator:latest

# Push images to registry
make docker-push IMG=<registry>/hsm-secrets-operator:tag

# Multi-architecture build and push
make docker-build-multiarch IMG=<registry>/hsm-secrets-operator:tag

# Build installer bundle (consolidated YAML)
make build-installer IMG=<registry>/hsm-secrets-operator:tag
```

### PKCS#11 Build Architecture

The project uses conditional compilation to support both production HSM environments and testing/CI:

**Production Build (`Dockerfile`):**
- **Manager**: CGO disabled, uses MockClient by default
- **Agent**: CGO enabled, includes real PKCS#11Client for HSM communication  
- **Discovery**: CGO disabled, native sysfs scanning only

**Testing Build (`Dockerfile.testing`):**
- **All binaries**: CGO disabled, uses MockClient stubs for PKCS#11
- **Benefits**: Faster builds, no C library dependencies, works in CI/testing

**Key Files:**
- `internal/hsm/pkcs11_client.go` - Real PKCS#11 implementation (requires CGO)
- `internal/hsm/pkcs11_client_nocgo.go` - Stub implementation (CGO disabled)
- Build tags automatically select the correct implementation

### CRD Management
```bash
# Generate CRDs and RBAC manifests
make manifests                # Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects
make generate                 # Generate DeepCopy methods for CRD types

# Install CRDs into cluster
make install                  # Install CRDs into the K8s cluster
make uninstall               # Uninstall CRDs from the K8s cluster

# Deploy operator to cluster
make deploy IMG=<some-registry>/hsm-secrets-operator:tag
make undeploy                # Remove operator from cluster
```

### Helm Chart Commands
```bash
# Lint Helm chart
helm lint helm/hsm-secrets-operator

# Template Helm chart for validation
helm template test helm/hsm-secrets-operator

# Install from local chart
helm install hsm-secrets-operator helm/hsm-secrets-operator \
  --namespace hsm-secrets-operator-system \
  --create-namespace

# Upgrade existing installation
helm upgrade hsm-secrets-operator helm/hsm-secrets-operator \
  --namespace hsm-secrets-operator-system

# Uninstall
helm uninstall hsm-secrets-operator -n hsm-secrets-operator-system

# Sync CRDs to Helm after changes
make helm-sync               # Copies CRDs from config/crd/bases/ to helm/hsm-secrets-operator/crds/
```

## Code Quality Requirements

**⚠️ CRITICAL: Always run these commands before making code changes:**

```bash
# RECOMMENDED: One command to run all quality checks
make quality

# OR run individually:
# 1. Format code (fixes spacing, imports, etc.)
gofmt -w .
# 2. Lint code (catches bugs, style issues, inefficiencies)  
golangci-lint run ./...
# 3. Run tests to ensure nothing broke
make test
```

**Why this matters:**
- `gofmt` ensures consistent formatting across the codebase
- `golangci-lint` catches potential bugs, inefficient code, and style violations (configured in `.golangci.yml`)
- Running these tools prevents CI/CD failures and maintains code quality
- **The status update loop bug** was caught by adding proper linting workflows

**Before committing any changes:**
1. ✅ `make quality` (format + vet + lint)
2. ✅ `make test` (all tests must pass)
3. ✅ Test your changes locally with actual deployment

## Common Development Workflows

### Adding a New CRD Field
```bash
# 1. Modify the CRD struct in api/v1alpha1/
# 2. Regenerate manifests and code
make manifests generate

# 3. Sync CRDs to Helm chart
make helm-sync

# 4. Run quality checks
make quality

# 5. Test the changes
make test
```

### Working with Controllers
```bash
# Run unit tests for specific controller
go test ./internal/controller -run TestHSMSecretController -v

# Test controller with actual cluster (local development)
make run

# Build and test with Docker
make docker-build IMG=test:latest
```

### API Development and Testing
```bash
# Test REST API endpoints locally (manager)
cd examples/api && ./health-check.sh

# Create test secrets via API
./create-secret.sh my-test-secret

# List all secrets
./list-secrets.sh
```

### gRPC Development and Testing
```bash
# Generate protobuf code after modifying .proto files
buf generate

# Test gRPC agent connectivity
# Note: Agent runs on port 9090 (gRPC) and 8093 (health)

# Test agent health via HTTP (from within cluster)
curl http://hsm-agent-pod:8093/healthz

# Test gRPC connection programmatically
# See internal/agent/grpc_integration_test.go for examples

# Protocol buffer linting
buf lint api/proto/hsm/v1/hsm.proto
```

### Protocol Buffer Development
```bash
# Install buf tool (required for proto generation)
go install github.com/bufbuild/buf/cmd/buf@latest

# Modify proto files
# Edit api/proto/hsm/v1/hsm.proto

# Regenerate Go code  
buf generate

# Format proto files
buf format -w api/proto/hsm/v1/hsm.proto

# Validate proto files
buf lint
```

## Project Overview

A Kubernetes operator that bridges Pico HSM binary data storage with Kubernetes Secrets, providing true secret portability through hardware-based storage. The operator implements a controller pattern that watches HSMSecret Custom Resource Definitions (CRDs) and maintains bidirectional synchronization between HSM binary data files and Kubernetes Secret objects.


## Architecture

### Four-Binary Architecture (Manager/Agent/Discovery Split + Race-Free HSMPool)

The operator uses a **four-binary architecture** with race-condition-free coordination for optimal security, resource usage, and deployment flexibility:

1. **Manager Binary** (`cmd/manager/main.go`)
   - Handles **HSMSecret CRDs** and secret synchronization  
   - Handles **HSMPool CRDs** for aggregating device discovery results
   - Uses **MockClient** by default (no PKCS#11 dependencies)
   - Includes REST API server for secret management
   - Runs as regular deployment (unprivileged)
   - Lightweight image, no HSM library dependencies

2. **Agent Binary** (`cmd/agent/main.go`)
   - Handles actual **HSM communication** via PKCS#11
   - Uses **real PKCS#11Client** for production HSM devices
   - Can fallback to **MockClient** for testing
   - Deployed close to HSM hardware (DaemonSet pattern)  
   - Heavy image with full PKCS#11 library dependencies
   - **gRPC API**: Serves HSM operations via gRPC on port 9090 (default)
   - **HTTP API**: Legacy HTTP support via `--use-grpc=false`
   - **Health Checks**: HTTP health endpoints on port 8093

3. **Discovery Binary** (`cmd/discovery/main.go`) 
   - Handles **HSMDevice CRDs** (readonly specs) and USB device discovery
   - **USB Detection Methods**:
     - **Native sysfs** (default): Reads `/sys/bus/usb/devices` directly like `lsusb` does internally
     - **Legacy sysfs**: Privileged scanning (backward compatibility only)
   - **Race-Free Coordination**: Reports via pod annotations instead of CRD status
   - **Ultra-lightweight**: No CGO, no external dependencies
   - **Security**: Runs non-privileged on Talos Linux with maximum hardening

4. **Test HSM Binary** (`cmd/test-hsm/main.go`)
   - Testing utility for HSM operations and debugging
   - Standalone tool for development and troubleshooting

### Controller Pattern

Each binary runs specific controllers:

- **HSMSecret Controller** (manager): `internal/controller/hsmsecret_controller.go`
  - Bidirectional sync between HSM and Kubernetes Secrets
  - Mock and PKCS#11 HSM client implementations
  - Owner reference management and finalizers

- **HSMPool Controller** (manager): `internal/controller/hsmpool_controller.go`
  - **NEW**: Aggregates device discovery reports from all discovery pods
  - **Race-Free**: Uses pod annotations for coordination instead of CRD status updates
  - Grace period handling for pod outages (default: 5 minutes)
  - Creates HSMPool CRDs automatically when HSMDevice CRDs are created

- **HSMPool Agent Controller** (manager): `internal/controller/hsmpool_agent_controller.go`
  - **NEW**: Manages agent deployment for HSM pools  
  - Coordinates agent pod lifecycle with discovered devices
  - **Enhanced**: Watches deployments for immediate recreation when deleted
  - Device path change detection and agent restart capability
  - Automatic cleanup of stale agents after device absence timeout (default: 10 minutes)

- **HSMDevice Controller** (discovery): `internal/controller/hsmdevice_controller.go` 
  - USB device discovery via sysfs scanning
  - **CRITICAL**: Fixed status update loops (see fixes section)
  - **NEW**: Reports discoveries via pod annotations instead of CRD status
  - Host path support for Talos Linux (`/host/sys`, `/host/dev`)
  - Well-known device specifications (Pico HSM, SmartCard-HSM)

### Data Flow & Reconciliation

**New Race-Free Architecture:**
```
HSM Storage ←→ HSMSecret CRD ←→ Kubernetes Secret
USB Device  ←→ HSMDevice CRD (readonly spec) ←→ Pod Annotations ←→ HSMPool CRD (aggregated status)

Manager:    HSMPath ←→ Agent gRPC ←→ PKCS#11 Client ←→ K8s Secret (owner refs)
            HSMDevice ←→ HSMPool (auto-created with owner refs)
            Pod Annotations ←→ HSMPool Status (aggregated discovery results)
            
Discovery:  /sys/bus/usb ←→ Pod Annotations (ephemeral reports)
Agent:      PKCS#11 Library ←→ HSM Device ←→ gRPC Server (port 9090)
```

**Key Benefits:**
- ✅ **No Race Conditions**: Each resource has single owner
- ✅ **Automatic Cleanup**: Pod dies → annotations disappear → no stale data  
- ✅ **Grace Periods**: 5-minute buffer prevents agent churn during outages
- ✅ **Kubernetes Native**: Standard patterns (annotations, owner refs, watches)

### gRPC Communication Architecture

The operator uses **Protocol Buffers (protobuf)** and **gRPC** for efficient, type-safe communication between manager and agent components:

**Protocol Definition**: `api/proto/hsm/v1/hsm.proto`
- **HSMAgent Service**: Complete gRPC service definition
- **10 Operations**: GetInfo, ReadSecret, WriteSecret, WriteSecretWithMetadata, ReadMetadata, DeleteSecret, ListSecrets, GetChecksum, IsConnected, Health
- **Type Safety**: Structured messages for HSMInfo, SecretData, SecretMetadata
- **Error Handling**: gRPC status codes for proper error propagation

**gRPC Server** (`internal/agent/grpc_server.go`):
- **Port 9090**: Default gRPC service port
- **Port 8093**: HTTP health checks (`/healthz`, `/readyz`)
- **Interceptors**: Request logging and metrics collection
- **Graceful Shutdown**: Context-based cancellation support

**gRPC Client** (`internal/agent/grpc_client.go`):
- **Connection Management**: Automatic keepalive and reconnection
- **Timeouts**: Configurable request timeouts (default: 30s)
- **Error Handling**: gRPC status code interpretation
- **Interface Compatibility**: Implements `hsm.Client` interface

**Protocol Buffer Generation**:
```bash
# Generate Go code from .proto files
buf generate

# Lint proto files
buf lint

# Format proto files  
buf format -w
```

**Generated Files**:
- `api/proto/hsm/v1/hsm.pb.go` - Message types
- `api/proto/hsm/v1/hsm_grpc.pb.go` - Service client/server code
- `hsm/v1/hsm.pb.go` - Duplicate for backward compatibility

### Key Architectural Patterns

1. **Status-Driven Reconciliation**: Controllers use comprehensive status fields to track state
   - `LastDiscoveryTime`, `LastSyncTime` 
   - Checksums for change detection (SHA256)
   - Kubernetes conditions for status reporting

2. **Client Abstraction**: `internal/hsm/client.go` interface supports multiple implementations
   - `MockClient` for testing with pre-populated secrets
   - `PKCS11Client` for production Pico HSM integration
   - Pluggable architecture for different HSM types

3. **Host System Integration**: Discovery requires privileged access
   - DaemonSet pattern for node-level USB scanning
   - Host path mounting (`/host/sys`, `/host/dev`, `/host/proc/bus/usb`)
   - Talos Linux compatibility with immutable filesystem

## Goals

### Primary Objectives
- **Simple KV Secrets**: Map HSM files to Kubernetes Secret objects (1:1 mapping)
- **Bidirectional Sync**: Changes in HSM automatically update Secret objects
- **Hardware Security**: Leverage Pico HSM's hardware-based protection
- **Secret Portability**: Enable moving secrets between clusters via HSM

### Key Features
- **Import Secrets**: Load existing secrets from HSM into Kubernetes
- **Edit Secrets**: Modify secrets without using cumbersome pkcs11-tool
- **Delete Secrets**: Remove secrets from both HSM and Kubernetes
- **Auto-Sync**: Detect HSM changes and update corresponding Secret objects

## CRD Structures

### HSMPool CRD Structure (NEW - Race-Free Device Aggregation)

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMPool
metadata:
  name: pico-hsm-pool
  ownerReferences:
    - kind: HSMDevice
      name: pico-hsm
spec:
  hsmDeviceRefs: ["pico-hsm"]          # References to HSMDevice specs
  gracePeriod: "5m"                    # Grace period for stale pod reports
  mirroring:                           # Optional mirroring configuration
    enabled: true
    primaryRole: "primary"
status:
  phase: "Ready"                       # Pending|Aggregating|Ready|Partial|Error
  totalDevices: 2
  availableDevices: 2
  expectedPods: 2                      # Expected discovery pods
  reportingPods:                       # Pods currently reporting
    - podName: "discovery-node1"
      nodeName: "worker-1"
      devicesFound: 1
      lastReportTime: "2025-08-19T10:00:00Z"
      discoveryStatus: "completed"
      fresh: true
    - podName: "discovery-node2"
      nodeName: "worker-2"
      devicesFound: 1
      lastReportTime: "2025-08-19T10:00:00Z"  
      discoveryStatus: "completed"
      fresh: true
  aggregatedDevices:                   # All discovered devices across cluster
    - devicePath: "/dev/bus/usb/001/015"
      nodeName: "worker-1"
      serialNumber: "DC6A33145E23A42A"
      available: true
      lastSeen: "2025-08-19T10:00:00Z"
  lastAggregationTime: "2025-08-19T10:00:00Z"
```

### HSMSecret CRD Structure

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: appname-secret                            # HSM path automatically set to this name
  namespace: appnamespace
spec:
  secretName: "appname-secret"                    # Target K8s Secret name (optional)
  autoSync: true                                  # Enable bidirectional sync (default: true)
  secretType: "Opaque"                           # Kubernetes Secret type (default: Opaque)
  syncInterval: 300                              # Sync interval in seconds (default: 300)
status:
  lastSyncTime: "2024-01-15T10:30:00Z"
  hsmChecksum: "sha256:abc123..."
  secretChecksum: "sha256:def456..."
  syncStatus: "InSync" | "OutOfSync" | "Error" | "Pending"
  lastError: "Error message if any"
  conditions: []                                 # Standard Kubernetes conditions
  secretRef:                                     # Reference to created Secret
    name: "appname-secret"
    namespace: "appnamespace"
```

### HSMDevice CRD Structure (Readonly Spec - No Status Updates)

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: my-pico-hsm
  namespace: secrets
spec:
  deviceType: "PicoHSM"
  
  # Discovery configuration (choose one method)
  discovery:
    # Option 1: USB discovery
    usb:
      vendorId: "20a0"
      productId: "4230"
    # Option 2: Device path discovery  
    # devicePath:
    #   path: "/dev/ttyUSB*"
    #   permissions: "0666"
    # Option 3: Auto-discovery based on device type
    # autoDiscovery: true
  
  # PKCS#11 configuration per device
  pkcs11:
    libraryPath: "/usr/lib/libsc-hsm-pkcs11.so"  # Example path - configure for your system
    slotId: 0
    pinSecret:
      name: "pico-hsm-pin"
      key: "pin"
      namespace: "secrets"  # Optional: cross-namespace secret
    tokenLabel: "MyHSM"     # Optional: specific token
  
  # Optional: target specific nodes
  nodeSelector:
    hsm-type: "pico"
  
  maxDevices: 1
# NOTE: No status field - HSMDevice is readonly spec only!
# Discovery results are reported via HSMPool CRD and pod annotations
```

### Pod Annotation Structure (Ephemeral Discovery Reports)

```yaml
# Discovery pods report their findings via annotations
apiVersion: v1
kind: Pod
metadata:
  name: discovery-node1
  annotations:
    hsm.j5t.io/device-report: |
      {
        "hsmDeviceName": "my-pico-hsm",
        "reportingNode": "worker-1",
        "discoveredDevices": [
          {
            "devicePath": "/dev/bus/usb/001/015",
            "serialNumber": "DC6A33145E23A42A",
            "lastSeen": "2025-08-19T10:00:00Z"
          }
        ],
        "lastReportTime": "2025-08-19T10:00:00Z",
        "discoveryStatus": "completed"
      }
```

## Implementation Strategy

### Phase 1: Basic Infrastructure ✅ COMPLETED
- [x] Initialize operator-sdk project structure
- [x] Define HSMSecret CRD with complete spec and status
- [x] Implement HSM client wrapper interface with PKCS#11 and Mock implementations
- [x] Create controller skeleton with full reconciliation logic

### Phase 2: Core Functionality ✅ COMPLETED
- [x] Implement HSM file reading/writing via client interface
- [x] Add Kubernetes Secret creation/update logic with owner references
- [x] Build complete reconciliation loop with finalizers
- [x] Add comprehensive error handling and logging

### Phase 3: Bidirectional Sync ✅ COMPLETED
- [x] Implement HSM sync via configurable polling intervals
- [x] Add SHA256 checksum-based change detection
- [x] Handle conflict resolution through status reporting
- [x] Add detailed status reporting with conditions and timestamps

### Phase 4: Secret Management Operations 🚧 IN PROGRESS
- [x] Import existing HSM secrets through HSMSecret CRDs
- [ ] Secret editing interface (kubectl plugin or annotations)
- [x] Secret deletion with proper cleanup via finalizers
- [ ] Bulk operations support

### Phase 5: USB Device Discovery ✅ COMPLETED
- [x] HSMDevice CRD for representing discovered HSM hardware
- [x] USB device discovery logic with sysfs scanning
- [x] Path-based device discovery with glob patterns
- [x] DaemonSet controller for node-level device scanning
- [x] Device plugin integration for Kubernetes resource allocation
- [x] Well-known HSM device specifications (Pico HSM, SmartCard-HSM)
- [x] Auto-discovery based on device types

## 🚨 Critical Fixes Applied

### Race Condition Elimination ✅ RESOLVED
**Problem**: Multiple discovery pods were fighting over HSMDevice CRD status updates, causing race conditions and reconciliation loops.

**Root Cause**: Multiple controllers updating the same CRD status simultaneously created race conditions and complex coordination.

**Solution Applied - New Architecture**: 
- **HSMDevice CRDs**: Now readonly specs only (no status field)
- **Pod Annotations**: Discovery pods report via their own annotations  
- **HSMPool CRDs**: Manager aggregates all pod reports into pool status
- **Owner References**: HSMPool is auto-created and owned by HSMDevice
- **Grace Periods**: 5-minute buffer prevents agent churn during outages
- **Result**: Zero race conditions, automatic cleanup, Kubernetes-native patterns

### Status Update Loop Fix ✅ RESOLVED  
**Problem**: HSMDevice controller was causing rapid reconciliation loops (spamming every millisecond) due to status updates triggering immediate re-reconciliation.

**Root Cause**: `LastDiscoveryTime` was updated with `metav1.Now()` on every reconcile, causing Kubernetes to detect resource changes and immediately schedule new reconciliation.

**Solution Applied**: 
- **File**: `internal/controller/hsmdevice_controller.go:294-389`
- **Logic**: Only update status when there are actual changes (device count, phase, etc.)
- **Time Updates**: Only update `LastDiscoveryTime` when significant changes occur or every 5+ minutes
- **Result**: Proper 30-second intervals instead of continuous loops
- **SUPERSEDED**: New architecture eliminates status updates entirely

### Architecture Separation ✅ COMPLETED
**Manager vs Discovery Split**:
- **Manager Binary** (`cmd/manager/main.go`): Handles HSMSecret CRDs, secret synchronization, API server
- **Discovery Binary** (`cmd/discovery/main.go`): Handles HSMDevice CRDs, USB device discovery
- **Separate Images**: 
  - `hsm-secrets-operator` (full image with HSM libraries)
  - `hsm-discovery` (lightweight distroless image)

### Per-Device Configuration Architecture ✅ COMPLETED
**Problem**: Global HSM configuration was inflexible for mixed HSM environments and created redundant configuration.

**Solution Applied**:
- **File**: `api/v1alpha1/hsmdevice_types.go`
- **New Structure**: Each HSMDevice CRD is completely self-contained with:
  - **Discovery Configuration**: USB vendor/product IDs, device paths, or auto-discovery
  - **PKCS#11 Configuration**: Library path, slot ID, PIN secrets, token labels
  - **Node Selection**: Target specific nodes with device-specific requirements
  - **Security**: Cross-namespace PIN secret references

**Benefits**:
- **Mixed Environments**: Support multiple HSM types with different libraries
- **Complete Isolation**: Each device has its own configuration and secrets
- **Flexibility**: USB discovery, device paths, or auto-discovery per device
- **Security**: Per-device PIN secrets with cross-namespace support

### Port Configuration Fix ✅ RESOLVED
**Problem**: API server port conflict with metrics server after configuration changes.

**Root Cause**: API server was configured to use port 8080, conflicting with metrics server.

**Solution Applied**:
- **Manager API Server**: Port 8090 (dedicated for REST API)
- **Manager Metrics Server**: Port 8080 internal, exposed as 8443 via service  
- **Manager Health Probes**: Port 8081 (unchanged)
- **Agent gRPC Server**: Port 9090 (default for HSM operations)
- **Agent Health Server**: Port 8093 (HTTP health checks)

**Result**: Clean port separation with no conflicts.

### Immediate Reconciliation for Resource Deletion ✅ COMPLETED (NEW)
**Enhancement**: All managed resources now support immediate recreation when manually deleted.

**Problem**: When users manually deleted agent deployments, discovery DaemonSets, or HSMPools, there could be delays before the controllers noticed and recreated them.

**Solution Applied**: 
- **Agent Deployments**: `HSMPoolAgentReconciler` now watches deployments with `findPoolsForDeployment()` mapping
- **Discovery DaemonSets**: `DiscoveryDaemonSetReconciler` now watches DaemonSets with `findDevicesForDaemonSet()` mapping  
- **HSMPools**: `DiscoveryDaemonSetReconciler` now watches HSMPools with `findDevicesForHSMPool()` mapping

**Implementation Pattern**:
```go
// All controllers now use this pattern for immediate reconciliation
.Watches(
    &ResourceType{},
    handler.EnqueueRequestsFromMapFunc(r.findOwnerFunction),
)
```

**Key Benefits**:
- ✅ **Immediate Recovery**: Delete any managed resource → immediate reconciliation → resource recreated
- ✅ **Label-Based Filtering**: Only resources with proper labels trigger reconciliation
- ✅ **Cross-Resource Mapping**: Deleted resource events map to correct owner for reconciliation
- ✅ **Comprehensive Coverage**: Agents, DaemonSets, and HSMPools all protected

**Files Updated**:
- `internal/controller/hsmpool_agent_controller.go`: Added deployment watching
- `internal/controller/discovery_daemonset_controller.go`: Added DaemonSet and HSMPool watching
- Comprehensive test coverage for all mapping functions

**Result**: Complete resilience to manual deletions across all infrastructure components.

## ✅ Current Implementation Status

### Completed Components

1. **HSMSecret CRD** (`api/v1alpha1/hsmsecret_types.go`)
   - Complete API definition with all fields and validation
   - Custom printer columns for `kubectl get hsmsecret` 
   - Short name support (`hsmsec`)
   - Comprehensive status tracking with checksums and conditions

2. **HSM Client Architecture** (`internal/hsm/`)
   - **Client Interface**: Flexible interface supporting multiple HSM implementations
   - **Mock Client**: Full testing implementation with pre-populated test secrets
   - **PKCS#11 Client**: Production-ready skeleton for real Pico HSM integration
   - **Checksum System**: SHA256 checksums for data integrity verification

3. **Controller Implementation**
   - **HSMSecret Controller** (`internal/controller/hsmsecret_controller.go`):
     - Complete reconciliation loop with error handling
     - Bidirectional sync between HSM and Kubernetes Secrets
     - Finalizer-based cleanup on HSMSecret deletion
     - Auto-sync with configurable intervals (default: 300s)
     - Owner references for proper garbage collection
   - **HSMDevice Controller** (`internal/controller/hsmdevice_controller.go`):
     - USB device discovery with proper requeue intervals
     - **FIXED**: Status update loop prevention (no more rapid reconciliation)
     - Host path support for Talos Linux (/host/sys, /host/dev)
     - Well-known device type auto-discovery

4. **USB Device Discovery & Mirroring** (`internal/discovery/`)
   - **USB Discoverer**: Scans sysfs for USB devices matching vendor/product IDs
   - **Path Discoverer**: Glob-based device path discovery (e.g., /dev/ttyUSB*)
   - **Device Manager**: Kubernetes resource allocation and device management
   - **Mirroring Manager**: Cross-node HSM device synchronization and failover
   - **Well-known Specs**: Built-in USB specifications for Pico HSM and SmartCard-HSM
   - **Topology Manager**: Primary/mirror device role assignment and health monitoring

5. **HSMDevice CRD** (`api/v1alpha1/hsmdevice_types.go`)
   - **Per-Device Configuration**: Complete self-contained device specifications
   - **Discovery Methods**: USB vendor/product IDs, device paths, or auto-discovery
   - **PKCS#11 Integration**: Library path, slot ID, PIN secrets, token labels per device
   - **Security Features**: Cross-namespace PIN secret references
   - **Node Targeting**: Device-specific node selectors
   - **Mirroring Support**: Cross-node high availability policies
   - **Backwards Compatibility**: Deprecated fields with migration path
   - **Custom Printer Columns**: Enhanced `kubectl get hsmdevice` output

6. **REST API Server** (`internal/api/`)
   - **Gin HTTP Server**: Complete REST API with all CRUD operations
   - **Secret Management**: Create, read, update, delete HSM secrets via HTTP
   - **Bulk Operations**: Import/export multiple secrets with JSON payloads
   - **Health & Metrics**: System health checks and operational metrics
   - **Error Handling**: Comprehensive error responses with detailed messages

6. **Production Features**
   - ✅ All unit tests passing
   - ✅ Docker image builds successfully
   - ✅ CRDs and RBAC manifests auto-generated
   - ✅ Sample HSMSecret and HSMDevice configurations provided
   - ✅ DaemonSet configuration for node-level device discovery
   - ✅ Proper RBAC permissions for Secrets, Events, and Device Discovery
   - ✅ Comprehensive logging and error handling

### Ready for Deployment

The operator can be immediately deployed and tested:

```bash
# Build both images
make docker-build IMG=hsm-secrets-operator:latest              # Manager
make docker-build-discovery DISCOVERY_IMG=hsm-discovery:latest # Discovery

# Deploy manager (handles HSMSecret CRDs and secret sync)
make deploy IMG=hsm-secrets-operator:latest

# Deploy discovery DaemonSet (handles HSMDevice CRDs and USB discovery)
kubectl apply -f deploy/talos/daemonset-discovery.yaml

# For Talos Linux specifically
kubectl apply -f examples/advanced/talos-deployment.yaml

# Test with sample resources
kubectl apply -f config/samples/hsm_v1alpha1_hsmsecret.yaml
kubectl apply -f config/samples/hsm_v1alpha1_hsmdevice.yaml

# Monitor status (should show proper 30s intervals, no rapid loops)
kubectl get hsmsecret -w
kubectl get hsmdevice -w
kubectl logs -n hsm-secrets-operator-system -l app=hsm-device-discovery
```

### Project Structure Overview

**Key Architecture Components:**
```
├── cmd/                           # Entry points for all binaries
│   ├── manager/main.go           # Manager: HSMSecret controller + API proxy
│   ├── discovery/main.go         # Discovery: HSMPool controller (removed from new arch)
│   ├── agent/main.go             # Agent: Direct HSM communication
│   └── test-hsm/main.go          # Test utility for HSM operations
├── api/                           # API definitions
│   ├── proto/hsm/v1/             # Protocol buffer definitions
│   │   ├── hsm.proto             # gRPC service definition
│   │   ├── hsm.pb.go             # Generated protobuf messages
│   │   └── hsm_grpc.pb.go        # Generated gRPC client/server
│   └── v1alpha1/                 # CRD definitions
│       ├── hsmsecret_types.go    # HSMSecret CRD
│       ├── hsmpool_types.go      # HSMPool CRD (race-free aggregation)
│       └── hsmdevice_types.go    # HSMDevice CRD (readonly specs)
├── internal/
│   ├── controller/               # Kubernetes controllers
│   │   ├── hsmsecret_controller.go        # Secret sync
│   │   ├── hsmpool_controller.go          # Device aggregation (NEW)
│   │   ├── hsmpool_agent_controller.go    # Agent deployment (NEW) 
│   │   └── discovery_daemonset_controller.go # DaemonSet and HSMPool management (Enhanced)
│   ├── hsm/                      # HSM client abstraction
│   │   ├── client.go             # Interface definition
│   │   ├── mock_client.go        # Testing implementation
│   │   ├── pkcs11_client.go      # Production PKCS#11 client (CGO)
│   │   └── pkcs11_client_nocgo.go # Stub for testing builds
│   ├── agent/                    # Agent deployment and communication
│   │   ├── deployment.go         # Agent pod management  
│   │   ├── server.go             # Legacy HTTP server
│   │   ├── grpc_server.go        # gRPC server implementation
│   │   ├── grpc_client.go        # gRPC client implementation
│   │   └── client.go             # Agent API client (legacy)
│   ├── api/                      # REST API server
│   │   ├── server.go             # HTTP server setup
│   │   └── proxy_handlers.go     # API proxy to agents
│   └── discovery/                # Device discovery (legacy)
│       ├── usb.go               # USB device scanning
│       └── mirroring.go         # Cross-node mirroring
├── examples/                     # Usage examples and configurations
│   ├── basic/                   # Simple configurations
│   ├── advanced/                # Complex multi-device setups
│   ├── api/                     # API usage scripts
│   └── agent-deployment/        # Agent-specific examples
├── config/                      # Kubernetes manifests
│   ├── crd/bases/              # Generated CRD definitions
│   ├── rbac/                   # Generated RBAC rules
│   ├── samples/                # Sample resource configurations
│   └── default/                # Default deployment configuration
├── helm/                       # Helm chart
│   └── hsm-secrets-operator/   # Complete Helm chart
├── buf.yaml                    # Buf protobuf tool configuration  
├── buf.gen.yaml                # Protobuf code generation config
├── hsm/v1/                     # Legacy protobuf output (compatibility)
│   ├── hsm.pb.go               # Duplicate protobuf messages
│   └── hsm_grpc.pb.go          # Duplicate gRPC client/server
└── test/                       # Test suites
    ├── e2e/                    # End-to-end tests
    └── utils/                  # Test utilities
```

## Technical Requirements

### Dependencies
- **operator-sdk**: For scaffolding and building the operator
- **controller-runtime**: Kubernetes controller framework
- **PKCS#11 library**: For HSM communication (sc-hsm-embedded)
- **OpenSC**: PKCS#11 middleware for smart cards/HSMs
- **buf**: Protocol buffer compiler and linter
- **protoc-gen-go**: Protocol buffer Go code generator
- **protoc-gen-go-grpc**: gRPC Go code generator
- **google.golang.org/grpc**: gRPC Go library

### HSM Integration
- Use PKCS#11 interface for Pico HSM communication
- Handle HSM authentication and session management
- Implement secure key storage and retrieval
- Support HSM-specific error handling

### PKCS#11 Library Compatibility

**⚠️ Important: Use OpenSC Library for Pico HSM**

The Pico HSM requires the **OpenSC PKCS#11 library** (`/usr/lib/opensc-pkcs11.so`) instead of the default CardContact library for proper data object support.

**Library Configuration:**
```yaml
# HSMDevice CRD configuration
spec:
  pkcs11:
    libraryPath: "/usr/lib/opensc-pkcs11.so"  # Use OpenSC instead of libsc-hsm-pkcs11.so
    slotId: 0
    pinSecret:
      name: "hsm-pin"
      key: "pin"
```

**Key Differences:**
- **CardContact Library** (`libsc-hsm-pkcs11.so`): Cryptographic operations only, no data object support
- **OpenSC Library** (`opensc-pkcs11.so`): Full PKCS#11 compliance with data object support

### Manual HSM Access with pkcs11-tool

**⚠️ Authentication Required**: HSM secrets created by the operator are marked as **private objects** and require PIN authentication to view.

**Common Operations:**

```bash
# List all secrets (requires PIN authentication)
pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin=$PKCS11_PIN --list-objects --type=data

# List public objects only (no PIN required)
pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --list-objects --type=data

# Read a specific secret component
pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin=$PKCS11_PIN --read-object --type=data --label="secret-name/api_key"

# Get HSM info
pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" -I

# List all object types
pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin=$PKCS11_PIN --list-objects
```

**Secret Storage Structure:**
- Each API secret becomes **multiple PKCS#11 data objects**
- Object naming: `secret-name/key-name` (e.g., `user-credentials/api_key`)
- Private objects: `flags: modifiable private` (require authentication)
- Public objects: `flags: modifiable` (visible without authentication)

**Emergency Access without Kubernetes:**
```bash
# Backup all secrets from pod
kubectl exec HSM_AGENT_POD -- sh -c '
  pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" \
    --login --pin="$PKCS11_PIN" \
    --list-objects --type=data > /tmp/hsm-backup.txt
'

# Copy backup file
kubectl cp HSM_AGENT_POD:/tmp/hsm-backup.txt ./hsm-secrets-backup.txt
```

### Kubernetes Integration
- Standard Secret object management
- RBAC for Secret read/write operations
- Event generation for audit trails
- Finalizers for cleanup on deletion

## Development Environment

### Container Setup
The Dockerfile builds an Alpine-based environment with:
- OpenSC development libraries
- PCSC-Lite for smart card communication
- sc-hsm-embedded library compilation
- USB device support

### Testing Strategy
- Unit tests for HSM client wrapper
- Integration tests with mock HSM
- End-to-end tests with real Pico HSM device
- Chaos testing for sync reliability

## Security Considerations

### HSM Security
- Private keys never leave the HSM
- All cryptographic operations performed on-device
- Hardware-based random number generation
- Tamper resistance and secure storage

### Kubernetes Security
- Principle of least privilege RBAC
- Secret encryption at rest (etcd)
- Network policies for HSM access
- Audit logging for all operations

### Operational Security
- HSM authentication management
- Certificate lifecycle management
- Backup and recovery procedures
- Key rotation strategies

## Monitoring and Observability

### Metrics
- Secret sync success/failure rates
- HSM operation latencies
- Secret object count and status
- Error rates by type

### Logging
- Structured logging with correlation IDs
- HSM operation audit trail
- Secret lifecycle events
- Performance metrics

### Alerting
- HSM connectivity issues
- Sync failures or conflicts
- Authentication failures
- Hardware errors

## Future Enhancements

### Advanced Features
- Multi-HSM support for high availability
- Cross-cluster secret replication
- Secret versioning and rollback
- Automated key rotation

### Integration Opportunities
- ArgoCD/GitOps integration
- Vault operator compatibility
- Service mesh certificate management
- CI/CD pipeline integration

## Getting Started

### Prerequisites
- Kubernetes cluster (v1.20+)
- Pico HSM device with configured partitions
- operator-sdk CLI tool
- kubectl access with appropriate RBAC

### Monitoring Operations
```bash
# View HSMSecret status with custom columns
kubectl get hsmsecret
kubectl get hsmsec  # Using short name

# View HSMDevice specifications (readonly)  
kubectl get hsmdevice
kubectl get hsmdev  # Using short name

# View HSMPool aggregated status (NEW)
kubectl get hsmpool
kubectl get hsmpool -o wide

# Describe for detailed information
kubectl describe hsmsecret database-credentials
kubectl describe hsmdevice pico-hsm-discovery
kubectl describe hsmpool pico-hsm-discovery-pool

# Check created secrets
kubectl get secrets -l managed-by=hsm-secrets-operator

# Monitor sync and discovery status
kubectl get hsmsecret database-credentials -o jsonpath='{.status.syncStatus}'
kubectl get hsmpool pico-hsm-discovery-pool -o jsonpath='{.status.phase}'

# View discovered devices (from HSMPool)
kubectl get hsmpool pico-hsm-discovery-pool -o jsonpath='{.status.aggregatedDevices[*].devicePath}'

# Monitor pod discovery reports (NEW)
kubectl get pods -l app.kubernetes.io/component=discovery \
  -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.annotations.hsm\.j5t\.io/device-report}{"\n"}{end}'

# Check pod reporting status in HSMPool
kubectl get hsmpool pico-hsm-discovery-pool -o jsonpath='{.status.reportingPods[*].podName}'
```

### Testing Immediate Reconciliation
```bash
# Test immediate recreation of managed resources

# 1. Delete an agent deployment - should recreate immediately
kubectl delete deployment -l app.kubernetes.io/component=agent
kubectl get deployments -l app.kubernetes.io/component=agent -w  # Watch immediate recreation

# 2. Delete a discovery DaemonSet - should recreate immediately  
kubectl delete daemonset -l app.kubernetes.io/component=discovery
kubectl get daemonsets -l app.kubernetes.io/component=discovery -w  # Watch immediate recreation

# 3. Delete an HSMPool - should recreate immediately
kubectl delete hsmpool <pool-name>
kubectl get hsmpool -w  # Watch immediate recreation

# All resources should be recreated within seconds of deletion
```

This operator design provides a secure, hardware-backed secret management solution that integrates seamlessly with Kubernetes while maintaining the security benefits of HSM-based storage.

## Quick Reference: Pico HSM + OpenSC

### API Testing (Recommended)
```bash
# Test API functionality with working HSM
cd /home/data/hsm-secrets-operator/examples/api

# Create a secret (HSM path = secret name)
./create-secret.sh my-test-secret

# List all secrets  
./list-secrets.sh

# Get specific secret via curl
curl http://localhost:8090/api/v1/hsm/secrets/my-test-secret | jq '.'
```

### Direct PKCS#11 Access
```bash
# Get agent pod name
AGENT_POD=$(kubectl get pods -l app.kubernetes.io/name=hsm-agent -o jsonpath='{.items[0].metadata.name}')

# List all secrets (requires authentication)
kubectl exec $AGENT_POD -- sh -c 'pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin="$PKCS11_PIN" --list-objects --type=data'

# Read specific secret component
kubectl exec $AGENT_POD -- sh -c 'pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" --login --pin="$PKCS11_PIN" --read-object --type=data --label="my-test-secret/api_key"'

# HSM device info
kubectl exec $AGENT_POD -- pkcs11-tool --module="/usr/lib/opensc-pkcs11.so" -I
```

### Agent Configuration and Ports
```bash
# Agent runs with gRPC by default (port 9090)
# Health checks via HTTP (port 8093)

# To use legacy HTTP mode instead of gRPC:
# agent --use-grpc=false --port=8090

# Check agent configuration
kubectl get deployment hsm-agent-* -o yaml | grep -A 10 containers:
```

### Troubleshooting
- **API works, pkcs11-tool doesn't see objects**: Use `--login --pin` for private objects
- **`CKR_DEVICE_REMOVED` errors**: Restart agent pod to reset PKCS#11 session
- **`CKR_TEMPLATE_INCONSISTENT` errors**: Switch from CardContact to OpenSC library
- **Agent crash loop**: Check library path and PIN secret configuration
- **gRPC connection failed**: Verify agent is running on port 9090, check service/endpoint configuration
- **Proto generation issues**: Install buf tool and run `buf generate` after proto changes