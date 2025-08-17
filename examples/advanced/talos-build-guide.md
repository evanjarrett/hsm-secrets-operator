# HSM Secrets Operator for Talos Linux

This guide shows how to deploy the HSM Secrets Operator on Talos Linux, which requires special handling for PKCS#11 libraries due to its immutable filesystem.

## Talos Linux Challenges

Talos Linux presents unique challenges for HSM integration:

1. **Immutable Root Filesystem**: Can't install libraries at runtime
2. **Minimal Base**: No package managers or build tools
3. **Container-Only**: All software must run in containers
4. **Security Focus**: Restricted permissions and capabilities

## Solutions for Talos

### Option 1: Custom Operator Image (Recommended)

Build the operator with PKCS#11 libraries included:

```bash
# Build custom operator image with libraries
docker build -f Dockerfile.talos -t hsm-secrets-operator:talos .

# Push to your registry
docker tag hsm-secrets-operator:talos your-registry.com/hsm-secrets-operator:talos
docker push your-registry.com/hsm-secrets-operator:talos
```

**Advantages**:
- Single image deployment
- Fast startup (no init containers)
- Libraries tested and verified
- Immutable and reproducible

**Use when**:
- You control the container registry
- You know which HSM vendors you'll use
- You want the simplest deployment

### Option 2: Init Container Pattern

Use init containers to provide libraries at runtime:

```bash
# Build library provider image
docker build -f Dockerfile.pkcs11-init -t pkcs11-libraries:latest .
docker push your-registry.com/pkcs11-libraries:latest
```

**Advantages**:
- Flexible library management
- Can update libraries without rebuilding operator
- Supports multiple vendor libraries
- Good for testing different libraries

**Use when**:
- You need flexibility for different HSM vendors
- You're still evaluating which libraries to use
- You want to update libraries independently

## Building for Talos

### Custom Operator Image Build

```bash
#!/bin/bash
# build-talos.sh - Build script for Talos deployment

set -e

REGISTRY=${REGISTRY:-"your-registry.com"}
TAG=${TAG:-"talos-$(date +%Y%m%d)"}
IMAGE_NAME="hsm-secrets-operator"

echo "Building HSM Secrets Operator for Talos Linux..."
echo "Registry: $REGISTRY"
echo "Tag: $TAG"

# Build the custom image with PKCS#11 libraries
docker build \
  -f Dockerfile.talos \
  -t $REGISTRY/$IMAGE_NAME:$TAG \
  -t $REGISTRY/$IMAGE_NAME:talos-latest \
  .

echo "Build completed successfully!"
echo "Images tagged:"
echo "  $REGISTRY/$IMAGE_NAME:$TAG"
echo "  $REGISTRY/$IMAGE_NAME:talos-latest"

# Push to registry
read -p "Push to registry? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    docker push $REGISTRY/$IMAGE_NAME:$TAG
    docker push $REGISTRY/$IMAGE_NAME:talos-latest
    echo "Images pushed to registry"
else
    echo "Skipping registry push"
fi
```

### Library Testing Script

```bash
#!/bin/bash
# test-libraries.sh - Test PKCS#11 libraries in container

CONTAINER_NAME="test-pkcs11-libs"
IMAGE_NAME="hsm-secrets-operator:talos"

echo "Testing PKCS#11 libraries in container..."

# Run container with library testing
docker run --rm --name $CONTAINER_NAME $IMAGE_NAME /bin/sh -c '
    echo "=== Testing PKCS#11 Libraries ==="
    echo "Library path: $LD_LIBRARY_PATH"
    echo "PKCS#11 module path: $PKCS11_MODULE_PATH"
    echo ""
    
    echo "=== Available Libraries ==="
    ls -la /usr/local/lib/pkcs11/
    echo ""
    
    echo "=== Library Dependencies ==="
    for lib in /usr/local/lib/pkcs11/*.so; do
        if [ -f "$lib" ]; then
            echo "Testing: $lib"
            ldd "$lib" 2>/dev/null || echo "  Static library or dependencies not found"
        fi
    done
    echo ""
    
    echo "=== PKCS#11 Function Check ==="
    for lib in /usr/local/lib/pkcs11/*.so; do
        if [ -f "$lib" ]; then
            echo "Checking PKCS#11 functions in: $lib"
            objdump -T "$lib" | grep -E "(C_GetFunctionList|C_Initialize)" | head -2
        fi
    done
'

echo "Library testing completed"
```

## Deployment on Talos

### 1. Node Preparation

Label your Talos nodes that have HSM devices:

```bash
# Label nodes with HSM hardware
kubectl label node talos-worker-1 hsm.j5t.io/enabled=true
kubectl label node talos-worker-2 hsm.j5t.io/enabled=true

# Verify node labels
kubectl get nodes --show-labels | grep hsm
```

### 2. Deploy Operator

```bash
# Apply CRDs first
kubectl apply -f config/crd/bases/

# Deploy with Talos-specific configuration
kubectl apply -f examples/advanced/talos-deployment.yaml

# Wait for deployment
kubectl wait --for=condition=available deployment/hsm-secrets-operator-controller-manager -n hsm-secrets-operator-system --timeout=300s
```

### 3. Verify Deployment

```bash
# Check operator status
kubectl get pods -n hsm-secrets-operator-system

# Check init container logs (if using init container pattern)
kubectl logs -n hsm-secrets-operator-system deployment/hsm-secrets-operator-controller-manager -c pkcs11-installer

# Check operator logs
kubectl logs -n hsm-secrets-operator-system deployment/hsm-secrets-operator-controller-manager -c manager

# Test HSM device discovery
kubectl get hsmdevice
kubectl describe hsmdevice talos-pico-hsm
```

### 4. Create Test Secret

```bash
# Create test HSMSecret
cat <<EOF | kubectl apply -f -
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: talos-test-secret
  namespace: default
spec:
  hsmPath: "secrets/talos/test-secret"
  secretName: "talos-test-secret"
  autoSync: true
  syncInterval: 300
EOF

# Check secret status
kubectl get hsmsecret talos-test-secret
kubectl get secret talos-test-secret
```

## Talos-Specific Configuration

### Machine Configuration

Add USB device access to your Talos machine configuration:

```yaml
# talos-machine-config.yaml
machine:
  kernel:
    modules:
      - name: usbcore
      - name: usb_common
      - name: usbhid
  
  # Allow USB device access
  sysctls:
    kernel.yama.ptrace_scope: 0
  
  # Device tree for USB HSM devices
  deviceTree:
    devices:
      - /dev/bus/usb

cluster:
  # Enable device plugins
  extraManifests:
    - https://raw.githubusercontent.com/kubernetes-sigs/node-feature-discovery/master/deployment/overlays/default/kustomization.yaml
```

### Talos Extensions (if needed)

For hardware-specific drivers, you might need custom Talos extensions:

```yaml
# talos-extensions.yaml
machine:
  install:
    extensions:
      - image: ghcr.io/siderolabs/intel-ucode:20230613
      - image: your-registry.com/hsm-driver-extension:v1.0.0
```

## Security Considerations for Talos

### Pod Security Standards

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: hsm-secrets-operator-system
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
```

### Network Policies

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: hsm-operator-talos-netpol
  namespace: hsm-secrets-operator-system
spec:
  podSelector:
    matchLabels:
      control-plane: controller-manager
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - namespaceSelector: {}
  egress:
  - to: []  # Allow all egress for K8s API and HSM communication
```

## Troubleshooting Talos Deployments

### Common Issues

1. **USB Device Access**
   ```bash
   # Check USB devices on Talos node
   talosctl -n NODE_IP dmesg | grep -i usb
   
   # List USB devices
   talosctl -n NODE_IP exec -- lsusb
   ```

2. **Library Loading Issues**
   ```bash
   # Check library in container
   kubectl exec -it deployment/hsm-secrets-operator-controller-manager -n hsm-secrets-operator-system -- ls -la /usr/local/lib/pkcs11/
   
   # Test library loading
   kubectl exec -it deployment/hsm-secrets-operator-controller-manager -n hsm-secrets-operator-system -- ldd /usr/local/lib/pkcs11/opensc-pkcs11.so
   ```

3. **Permission Issues**
   ```bash
   # Check container security context
   kubectl get pod -n hsm-secrets-operator-system -o yaml | grep -A 10 securityContext
   
   # Check device permissions
   kubectl exec -it deployment/hsm-secrets-operator-controller-manager -n hsm-secrets-operator-system -- ls -la /dev/bus/usb/
   ```

### Debug Commands

```bash
# Talos system information
talosctl -n NODE_IP version
talosctl -n NODE_IP get members

# Container runtime information
talosctl -n NODE_IP containers

# Kubernetes node information
kubectl describe node TALOS_NODE

# HSM operator debugging
kubectl logs -f -n hsm-secrets-operator-system deployment/hsm-secrets-operator-controller-manager

# HSM device status
kubectl get hsmdevice -o yaml
```

## Performance Optimization for Talos

### Resource Management

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
    # Request specific resources for HSM devices
    vendor.com/hsm: 1
  limits:
    cpu: 1000m
    memory: 512Mi
    vendor.com/hsm: 1
```

### Node Affinity

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
      - matchExpressions:
        - key: hsm.j5t.io/enabled
          operator: In
          values: ["true"]
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
```

This comprehensive guide provides everything needed to successfully deploy the HSM Secrets Operator on Talos Linux with proper PKCS#11 library support!