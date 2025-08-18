# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Kubernetes operator that bridges Pico HSM binary data storage with Kubernetes Secrets, providing true secret portability through hardware-based storage. The operator implements a controller pattern that watches HSMSecret Custom Resource Definitions (CRDs) and maintains bidirectional synchronization between HSM binary data files and Kubernetes Secret objects.

## Development Commands

### Operator SDK Commands
```bash
# Initialize operator project (if not already done)
operator-sdk init --domain=j5t.io --repo=github.com/evanjarrett/hsm-secrets-operator

# Create new APIs/CRDs
operator-sdk create api --group=hsm --version=v1alpha1 --kind=HSMSecret

# Generate manifests and code
make generate
make manifests

# Build and test
make build
make test

# Docker operations
make docker-build IMG=<registry>/hsm-secrets-operator:latest
make docker-push IMG=<registry>/hsm-secrets-operator:latest

# Deploy to cluster
make deploy IMG=<registry>/hsm-secrets-operator:latest
make undeploy
```

### Development Workflow
```bash
# Run operator locally for development
make install    # Install CRDs
make run        # Run controller locally

# Test with sample resources
kubectl apply -f config/samples/

# View logs
kubectl logs -f deployment/hsm-secrets-operator-controller-manager -n hsm-secrets-operator-system
```

## Architecture

### Core Components

1. **HSMSecret CRD**: Custom resource definition that represents a secret stored on the Pico HSM
2. **HSMSecret Controller**: Watches HSMSecret resources and manages synchronization
3. **HSM Client**: PKCS#11 interface wrapper for Pico HSM communication
4. **Secret Manager**: Handles Kubernetes Secret object lifecycle

### Data Flow

```
Pico HSM (binary files) <-> HSMSecret CRD <-> Kubernetes Secret
    secrets/appnamespace/appname-secret  ->  appnamespace/appname-secret
```

### Controller Pattern

The operator follows the standard Kubernetes controller pattern:
- **Watch**: Monitor HSMSecret CRDs and HSM file changes
- **Reconcile**: Ensure desired state matches actual state
- **Update**: Sync changes bidirectionally between HSM and K8s Secrets

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

## HSMSecret CRD Structure

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: appname-secret
  namespace: appnamespace
spec:
  hsmPath: "secrets/appnamespace/appname-secret"  # Path on Pico HSM
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

### Status Update Loop Fix ✅ RESOLVED
**Problem**: HSMDevice controller was causing rapid reconciliation loops (spamming every millisecond) due to status updates triggering immediate re-reconciliation.

**Root Cause**: `LastDiscoveryTime` was updated with `metav1.Now()` on every reconcile, causing Kubernetes to detect resource changes and immediately schedule new reconciliation.

**Solution Applied**: 
- **File**: `internal/controller/hsmdevice_controller.go:294-389`
- **Logic**: Only update status when there are actual changes (device count, phase, etc.)
- **Time Updates**: Only update `LastDiscoveryTime` when significant changes occur or every 5+ minutes
- **Result**: Proper 30-second intervals instead of continuous loops

### Architecture Separation ✅ COMPLETED
**Manager vs Discovery Split**:
- **Manager Binary** (`cmd/manager/main.go`): Handles HSMSecret CRDs, secret synchronization, API server
- **Discovery Binary** (`cmd/discovery/main.go`): Handles HSMDevice CRDs, USB device discovery
- **Separate Images**: 
  - `hsm-secrets-operator` (full image with HSM libraries)
  - `hsm-discovery` (lightweight distroless image)

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
   - Complete device discovery specification with USB and path-based options
   - Auto-discovery based on device types with well-known specifications
   - Mirroring policies for cross-node high availability
   - Node selector support for targeted discovery
   - Comprehensive status tracking with discovered device details
   - Custom printer columns for `kubectl get hsmdevice`

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

### Complete Files Structure
```
├── api/v1alpha1/
│   ├── hsmsecret_types.go          # HSMSecret CRD with mirroring support
│   ├── hsmdevice_types.go          # HSMDevice CRD with USB discovery
│   └── groupversion_info.go        # API group metadata
├── cmd/
│   ├── manager/main.go             # Manager binary (HSMSecret controller + API)
│   └── discovery/main.go           # Discovery binary (HSMDevice controller)
├── internal/
│   ├── controller/
│   │   ├── hsmsecret_controller.go # Secret reconciliation with fallback
│   │   └── hsmdevice_controller.go # Device discovery (FIXED: no more loops!)
│   ├── discovery/
│   │   ├── usb.go                  # USB device discovery (Talos host path support)
│   │   ├── mirroring.go            # Cross-node device mirroring
│   │   └── deviceplugin.go         # Kubernetes device management
│   ├── hsm/
│   │   ├── client.go               # HSM client interface
│   │   ├── mock_client.go          # Full test implementation
│   │   └── pkcs11_client.go        # Production PKCS#11 client
│   └── api/
│       ├── server.go               # REST API server with Gin
│       ├── handlers.go             # HTTP request handlers
│       └── middleware.go           # API middleware
├── examples/
│   ├── basic/                      # Basic usage examples
│   ├── advanced/                   # Advanced configurations
│   │   ├── talos-deployment.yaml   # Talos Linux deployment
│   │   ├── talos-build-guide.md    # Talos setup guide
│   │   └── custom-library-guide.md # PKCS#11 library integration
│   └── api/                        # API usage examples
│       ├── bulk-operations.sh      # Basic bulk operations
│       ├── advanced-bulk-import.sh # Advanced bulk import
│       ├── direct-import-examples.sh # Direct API examples
│       ├── production-import.json  # Sample production config
│       └── bulk-secrets.json       # Sample bulk config
├── scripts/
│   └── build-talos.sh             # Talos Linux build automation
├── deploy/
│   └── talos/                     # Talos-specific manifests
│       ├── daemonset-discovery.yaml # Fixed discovery DaemonSet (no loops!)
│       └── README.md               # Talos deployment guide
├── config/
│   ├── crd/bases/                 # Generated CRD manifests
│   ├── rbac/                      # Generated RBAC rules
│   └── samples/                   # Sample resources
├── Dockerfile                     # Manager image (with HSM libraries)
├── Dockerfile.discovery          # Discovery image (lightweight distroless)
├── Dockerfile.talos              # Talos-optimized image
├── test-loop-fix.yaml            # Test pod for verifying loop fix
├── STATUS-UPDATE-LOOP-FIX.md     # Documentation of critical fix
└── CLAUDE.md                     # This file (updated with fixes!)
```

## Technical Requirements

### Dependencies
- **operator-sdk**: For scaffolding and building the operator
- **controller-runtime**: Kubernetes controller framework
- **PKCS#11 library**: For HSM communication (sc-hsm-embedded)
- **OpenSC**: PKCS#11 middleware for smart cards/HSMs

### HSM Integration
- Use PKCS#11 interface for Pico HSM communication
- Handle HSM authentication and session management
- Implement secure key storage and retrieval
- Support HSM-specific error handling

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

# View HSMDevice status with custom columns  
kubectl get hsmdevice
kubectl get hsmdev  # Using short name

# Describe for detailed information
kubectl describe hsmsecret database-credentials
kubectl describe hsmdevice pico-hsm-discovery

# Check created secrets
kubectl get secrets -l managed-by=hsm-secrets-operator

# Monitor sync and discovery status
kubectl get hsmsecret database-credentials -o jsonpath='{.status.syncStatus}'
kubectl get hsmdevice pico-hsm-discovery -o jsonpath='{.status.phase}'

# View discovered devices
kubectl get hsmdevice pico-hsm-discovery -o jsonpath='{.status.discoveredDevices[*].devicePath}'
```

This operator design provides a secure, hardware-backed secret management solution that integrates seamlessly with Kubernetes while maintaining the security benefits of HSM-based storage.