# New Race Condition-Free Architecture

## Overview

The HSM Secrets Operator has been refactored to eliminate race conditions through clear separation of concerns and ephemeral coordination using pod annotations.

## Architecture Components

### 1. HSMDevice (Readonly Spec)
```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: pico-hsm
spec:
  deviceType: "PicoHSM"
  discovery:
    autoDiscovery: true
  pkcs11:
    libraryPath: "/usr/lib/libsc-hsm-pkcs11.so"
    slotId: 0
    pinSecret:
      name: "pico-hsm-pin"
      key: "pin"
```

**Purpose**: Device specification and configuration only. No dynamic status.

### 2. HSMPool (Aggregated Status)
```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMPool  
metadata:
  name: pico-hsm-pool
  ownerReferences:
    - kind: HSMDevice
      name: pico-hsm
spec:
  hsmDeviceRef: pico-hsm
  gracePeriod: 5m
status:
  phase: Ready
  totalDevices: 2
  availableDevices: 2
  reportingPods:
    - podName: discovery-node1
      devicesFound: 1
      fresh: true
    - podName: discovery-node2  
      devicesFound: 1
      fresh: true
```

**Purpose**: Aggregates discovery results from all pods with grace periods for outages.

### 3. Pod Annotations (Ephemeral Reports)
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: discovery-node1
  annotations:
    hsm.j5t.io/device-report: |
      {
        "hsmDeviceName": "pico-hsm",
        "reportingNode": "node1",
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

**Purpose**: Each discovery pod reports its findings via its own annotations. Auto-cleanup when pods disappear.

## Data Flow

```
1. User creates HSMDevice → Manager creates HSMPool
2. Discovery pods see HSMDevice → Update own annotations
3. HSMPool controller watches annotations → Aggregates into pool status  
4. Pool status shows complete device availability across cluster
```

## Benefits

✅ **No Race Conditions**: Each resource has single owner
✅ **Automatic Cleanup**: Pod dies → annotations disappear → no stale data  
✅ **Grace Periods**: 5-minute buffer prevents agent churn during outages
✅ **Kubernetes Native**: Standard patterns (annotations, owner refs, watches)
✅ **Scalable**: Works with any number of discovery pods

## Migration Guide

### Old vs New

**Before**: 
- HSMDevice had complex status with coordination
- Multiple pods fought over same status
- Race conditions and reconciliation loops

**After**:
- HSMDevice: readonly spec only
- HSMPool: aggregated status 
- Pod annotations: ephemeral reports

### Deployment Changes

1. **New CRDs**: HSMPool CRD added alongside HSMDevice
2. **Pod Environment**: Discovery pods need `POD_NAME` and `POD_NAMESPACE` env vars
3. **RBAC**: Added permissions for HSMPools and pod annotations

### Expected Behavior

```bash
# Create HSMDevice
kubectl apply -f examples/new-architecture/test-hsmdevice.yaml

# Manager auto-creates HSMPool  
kubectl get hsmpool
# NAME                HSMDEVICE      TOTAL   AVAILABLE   PHASE
# pico-hsm-test-pool  pico-hsm-test  2       2          Ready

# Check pod reports
kubectl get pods -l app.kubernetes.io/component=discovery \
  -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.annotations.hsm\.j5t\.io/device-report}{"\n"}{end}'

# Pool aggregates all reports  
kubectl get hsmpool pico-hsm-test-pool -o yaml
```

The new architecture is production-ready and eliminates all race conditions while providing clear visibility into device discovery across the cluster.