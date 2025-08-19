# HSM Secrets Operator Helm Chart

A Kubernetes operator that bridges Pico HSM binary data storage with Kubernetes Secrets, providing true secret portability through hardware-based storage.

## Prerequisites

- Kubernetes 1.20+
- Helm 3.0+
- (Optional) Prometheus Operator for metrics collection

## Installing the Chart

To install the chart with the release name `hsm-secrets-operator`:

```bash
helm install hsm-secrets-operator ./helm/hsm-secrets-operator
```

## Uninstalling the Chart

To uninstall/delete the `hsm-secrets-operator` deployment:

```bash
helm uninstall hsm-secrets-operator
```

The command removes all the Kubernetes components associated with the chart and deletes the release.

## Configuration

The following table lists the configurable parameters of the HSM Secrets Operator chart and their default values.

### Image Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Operator image repository | `hsm-secrets-operator` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `image.tag` | Image tag (defaults to chart appVersion) | `""` |
| `imagePullSecrets` | Image pull secrets | `[]` |

### Controller Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controllerManager.replicas` | Number of controller replicas | `1` |
| `controllerManager.resources.limits.cpu` | CPU limit | `500m` |
| `controllerManager.resources.limits.memory` | Memory limit | `128Mi` |
| `controllerManager.resources.requests.cpu` | CPU request | `10m` |
| `controllerManager.resources.requests.memory` | Memory request | `64Mi` |

### HSM Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `hsm.pkcs11.library` | PKCS#11 library path | `/usr/lib/x86_64-linux-gnu/opensc-pkcs11.so` |
| `hsm.pkcs11.slotId` | HSM slot ID | `0` |
| `hsm.pkcs11.pinSecret.name` | Secret name containing HSM PIN | `hsm-pin` |
| `hsm.pkcs11.pinSecret.key` | Secret key containing HSM PIN | `pin` |

### Discovery Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `discovery.enabled` | Enable device discovery DaemonSet | `true` |
| `discovery.usb.enabled` | Enable USB device discovery | `true` |
| `discovery.usb.deviceTypes` | List of HSM device types to discover | `["pico-hsm", "smartcard-hsm"]` |
| `discovery.path.enabled` | Enable path-based discovery | `true` |
| `discovery.path.patterns` | Device path patterns to scan | `["/dev/ttyUSB*", "/dev/hidraw*"]` |

### API Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `api.enabled` | Enable REST API server | `true` |
| `api.port` | API server port | `8080` |

### Metrics Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metrics.enabled` | Enable metrics collection | `true` |
| `metrics.serviceMonitor.enabled` | Create ServiceMonitor for Prometheus | `false` |
| `metrics.serviceMonitor.namespace` | ServiceMonitor namespace | `""` |
| `metrics.serviceMonitor.labels` | ServiceMonitor labels | `{}` |

### RBAC Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.annotations` | Service account annotations | `{}` |
| `serviceAccount.name` | Service account name | `""` |
| `rbac.create` | Create RBAC resources | `true` |

### Resource Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `hsmsecret.enabled` | Create example HSMSecret resources | `false` |
| `hsmdevice.enabled` | Create example HSMDevice resources | `false` |

### Other Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `crds.install` | Install CRDs | `true` |
| `crds.keep` | Keep CRDs on uninstall | `true` |
| `config.defaultSyncInterval` | Default sync interval (seconds) | `300` |
| `config.defaultSecretType` | Default Kubernetes secret type | `Opaque` |
| `config.verboseLogging` | Enable verbose logging | `false` |

## Usage Examples

### Basic Installation

```bash
# Install with default settings (mock HSM for testing)
helm install hsm-secrets-operator ./helm/hsm-secrets-operator
```

### Production Installation with PKCS#11

```bash
# Create HSM PIN secret first
kubectl create secret generic hsm-pin --from-literal=pin=your-hsm-pin

# Install operator (HSM configuration is now per-device via HSMDevice CRDs)
helm install hsm-secrets-operator ./helm/hsm-secrets-operator
```

### Installation with Resources

```bash
# Install with example resources
helm install hsm-secrets-operator ./helm/hsm-secrets-operator \
  --set hsmsecret.enabled=true \
  --set hsmdevice.enabled=true
```

### Installation with Prometheus Monitoring

```bash
# Install with ServiceMonitor for Prometheus
helm install hsm-secrets-operator ./helm/hsm-secrets-operator \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.serviceMonitor.namespace=monitoring
```

## Custom Resources

After installation, you can create HSM secrets using the custom resources:

### HSMSecret Example

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: database-credentials
  namespace: production
spec:
  hsmPath: "secrets/production/database-credentials"
  secretName: "database-credentials"
  autoSync: true
  syncInterval: 300
  secretType: Opaque
```

### HSMDevice Example

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMDevice
metadata:
  name: pico-hsm-discovery
  namespace: hsm-secrets-operator-system
spec:
  deviceType: "pico-hsm"
  discovery:
    usb:
      enabled: true
      vendorId: "1234"
      productId: "5678"
```

## Troubleshooting

### Check Operator Status

```bash
kubectl get deployment hsm-secrets-operator-controller-manager
kubectl logs -f deployment/hsm-secrets-operator-controller-manager
```

### Check Custom Resources

```bash
kubectl get hsmsecrets --all-namespaces
kubectl get hsmdevices --all-namespaces
kubectl describe hsmsecret <name> -n <namespace>
```

### Check Device Discovery

```bash
kubectl get daemonset hsm-secrets-operator-discovery
kubectl logs daemonset/hsm-secrets-operator-discovery
```

## Development

To modify and test the chart:

```bash
# Lint the chart
helm lint ./helm/hsm-secrets-operator

# Test template rendering
helm template hsm-secrets-operator ./helm/hsm-secrets-operator

# Install with dry-run
helm install hsm-secrets-operator ./helm/hsm-secrets-operator --dry-run --debug
```