# Custom PKCS#11 Library Integration Guide

This guide shows different approaches to integrate custom PKCS#11 libraries with the HSM Secrets Operator.

## Overview

The operator needs access to PKCS#11 libraries to communicate with HSM devices. While common libraries like OpenSC are included by default, custom or vendor-specific libraries require additional setup.

## Available Approaches

### Method 1: Simple Configuration (Easiest)
**Best for**: Standard libraries already available on nodes

Simply specify the library path in your HSMDevice:

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: my-hsm
spec:
  deviceType: Generic
  pkcs11LibraryPath: "/usr/local/lib/libmyhsm.so"
```

**Requirements**:
- Library must be pre-installed on all nodes
- Same path on all nodes
- Proper permissions (readable by operator)

---

### Method 2: Init Container (Recommended)
**Best for**: Libraries that can be downloaded/installed at runtime

```yaml
initContainers:
- name: install-pkcs11
  image: alpine:latest
  command: ["/install-library.sh"]
  volumeMounts:
  - name: shared-libs
    mountPath: /shared
```

**Advantages**:
- Self-contained deployment
- Version controlled libraries
- Works with custom images

**Use cases**:
- Vendor libraries available via download
- Custom compiled libraries
- Version-specific library requirements

---

### Method 3: Sidecar Container (Advanced)
**Best for**: Complex library management or multiple libraries

```yaml
containers:
- name: pkcs11-provider
  image: custom-pkcs11-provider:latest
  volumeMounts:
  - name: pkcs11-libs
    mountPath: /shared-libs
```

**Advantages**:
- Dedicated library management
- Hot-swappable libraries
- Isolation from main operator

**Use cases**:
- Multiple vendor libraries
- Libraries requiring specific runtime environments
- Complex licensing scenarios

---

### Method 4: DaemonSet Installation (Node-level)
**Best for**: System-level library installation

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: pkcs11-installer
spec:
  template:
    spec:
      hostNetwork: true
      containers:
      - name: installer
        securityContext:
          privileged: true
```

**Advantages**:
- Libraries available to all pods on node
- Persistent across pod restarts
- System-level integration

**Use cases**:
- System drivers required
- Shared across multiple applications
- Hardware-specific installations

## Implementation Examples

### Example 1: YubiKey Integration

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: yubikey-hsm
spec:
  deviceType: Generic
  usb:
    vendorId: "1050"  # Yubico
    productId: "0407"  # YubiKey 4/5 Series
  pkcs11LibraryPath: "/usr/lib/x86_64-linux-gnu/libykcs11.so.1"
  nodeSelector:
    yubikey.enabled: "true"
```

### Example 2: SoftHSM (Software HSM)

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: softhsm
spec:
  deviceType: Generic
  devicePath:
    path: "/var/lib/softhsm/tokens/*"
    permissions: "0644"
  pkcs11LibraryPath: "/usr/lib/softhsm/libsofthsm2.so"
```

### Example 3: Custom Vendor HSM

```yaml
# ConfigMap with vendor-specific configuration
apiVersion: v1
kind: ConfigMap
metadata:
  name: vendor-hsm-config
data:
  library-path: "/opt/vendor-hsm/lib/libvendor-pkcs11.so"
  slot-config: |
    slot_0 = /dev/vendor-hsm0
    slot_1 = /dev/vendor-hsm1

---
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: vendor-hsm
spec:
  deviceType: Generic
  usb:
    vendorId: "ABCD"
    productId: "1234"
  pkcs11LibraryPath: "/opt/vendor-hsm/lib/libvendor-pkcs11.so"
  nodeSelector:
    vendor-hsm.installed: "true"
```

## Custom Container Images

### Building a Custom Operator Image

```dockerfile
# Dockerfile for custom operator with additional libraries
FROM hsm-secrets-operator:latest

# Install additional PKCS#11 libraries
RUN apt-get update && apt-get install -y \
    libykcs11-1 \        # YubiKey library
    softhsm2 \           # SoftHSM
    opensc-pkcs11 \      # OpenSC (if not included)
    && apt-get clean

# Copy custom vendor libraries
COPY vendor-libs/* /usr/local/lib/
RUN ldconfig

# Copy custom configurations
COPY pkcs11-configs/* /etc/pkcs11/

USER 65532:65532
ENTRYPOINT ["/manager"]
```

### Building Library Provider Sidecar

```dockerfile
# Dockerfile for PKCS#11 library provider sidecar
FROM alpine:latest

# Install vendor-specific libraries
RUN apk add --no-cache wget unzip

# Download vendor library
RUN wget -O /tmp/vendor-lib.zip https://vendor.com/pkcs11-lib.zip && \
    unzip /tmp/vendor-lib.zip -d /vendor-libs && \
    chmod 755 /vendor-libs/*.so

# Copy script to provide libraries
COPY provide-libs.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/provide-libs.sh

ENTRYPOINT ["/usr/local/bin/provide-libs.sh"]
```

## Configuration Patterns

### Environment-Specific Libraries

```yaml
# Development environment
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: dev-hsm
  namespace: development
spec:
  deviceType: Generic
  pkcs11LibraryPath: "/usr/lib/softhsm/libsofthsm2.so"  # Software HSM for dev

---
# Production environment
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: prod-hsm
  namespace: production
spec:
  deviceType: PicoHSM
  pkcs11LibraryPath: "/usr/lib/x86_64-linux-gnu/pkcs11/opensc-pkcs11.so"  # Hardware HSM
```

### Multi-Vendor Support

```yaml
# HSM Device for Vendor A
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: vendor-a-hsm
spec:
  deviceType: Generic
  usb:
    vendorId: "1111"
    productId: "AAAA"
  pkcs11LibraryPath: "/opt/vendor-a/lib/libvendor-a-pkcs11.so"
  nodeSelector:
    hsm.vendor: "vendor-a"

---
# HSM Device for Vendor B
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: vendor-b-hsm
spec:
  deviceType: Generic
  usb:
    vendorId: "2222"
    productId: "BBBB"
  pkcs11LibraryPath: "/opt/vendor-b/lib/libvendor-b-pkcs11.so"
  nodeSelector:
    hsm.vendor: "vendor-b"
```

## Troubleshooting

### Common Issues

1. **Library Not Found**
   ```bash
   # Check if library exists
   ls -la /path/to/library.so
   
   # Check library dependencies
   ldd /path/to/library.so
   
   # Test library loading
   pkcs11-tool --module /path/to/library.so --list-slots
   ```

2. **Permission Issues**
   ```bash
   # Check file permissions
   ls -la /path/to/library.so
   
   # Fix permissions if needed
   chmod 755 /path/to/library.so
   
   # Check SELinux context (if applicable)
   ls -Z /path/to/library.so
   ```

3. **Library Compatibility**
   ```bash
   # Check architecture
   file /path/to/library.so
   
   # Check for missing symbols
   nm -D /path/to/library.so | grep C_GetFunctionList
   
   # Test basic functionality
   pkcs11-tool --module /path/to/library.so --list-mechanisms
   ```

### Debug Commands

```bash
# Check HSMDevice status
kubectl describe hsmdevice my-hsm

# Check operator logs
kubectl logs -n hsm-secrets-operator-system deployment/hsm-secrets-operator-controller-manager

# Test PKCS#11 library directly
kubectl exec -it hsm-operator-pod -- pkcs11-tool --module /path/to/library.so --list-slots

# Check library loading in container
kubectl exec -it hsm-operator-pod -- ldd /path/to/library.so
```

## Best Practices

### Security
- Use minimal container images with only required libraries
- Verify library checksums/signatures
- Use read-only mounts where possible
- Apply least privilege principles

### Performance
- Pre-install libraries in base images when possible
- Use shared volumes for multiple containers
- Cache downloaded libraries
- Minimize init container overhead

### Maintenance
- Version pin all library dependencies
- Test library compatibility before deployment
- Monitor library load times and errors
- Keep backup copies of working libraries

### Testing
- Test library loading in isolation
- Validate all PKCS#11 operations
- Test failover scenarios
- Verify performance under load