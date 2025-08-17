# Deployment Examples

This directory contains complete deployment examples for the HSM Secrets Operator in production environments.

## Files

- **[complete-setup.yaml](complete-setup.yaml)** - Full production deployment with all components
- **[operator-deployment.yaml](operator-deployment.yaml)** - Just the operator deployment
- **[monitoring-setup.yaml](monitoring-setup.yaml)** - Prometheus monitoring configuration

## Complete Setup

The `complete-setup.yaml` file demonstrates a full production deployment including:

### Core Components
- HSM Secrets Operator deployment
- HSMDevice configuration with mirroring
- Production HSMSecret resources
- RBAC configuration

### Sample Application
- Web application deployment using HSM secrets
- Database credentials from HSM
- TLS certificates from HSM
- High availability configuration

### Production Features
- Pod anti-affinity for distribution across nodes
- Horizontal Pod Autoscaler for scaling
- Pod Disruption Budget for availability
- Network policies for security
- Resource limits and health checks

### Monitoring
- ServiceMonitor for Prometheus integration
- Metrics collection from operator
- HSM device health monitoring

## Deployment Steps

### 1. Prerequisites

Ensure you have:
- Kubernetes cluster (v1.20+)
- HSM devices connected to nodes
- OpenSC libraries installed
- Prometheus Operator (for monitoring)

### 2. Label HSM Nodes

Label nodes that have HSM devices:
```bash
kubectl label node <node-name> hsm.j5t.io/enabled=true
```

### 3. Deploy the Operator

```bash
# Deploy CRDs first
kubectl apply -f config/crd/bases/

# Deploy the operator
kubectl apply -f config/default/

# Or use the complete setup
kubectl apply -f examples/deployment/complete-setup.yaml
```

### 4. Verify Deployment

```bash
# Check operator pods
kubectl get pods -n hsm-secrets-operator-system

# Check HSM devices
kubectl get hsmdevice

# Check secrets
kubectl get hsmsecret
kubectl get secret
```

### 5. Test the API

If API is enabled:
```bash
# Port forward to access API locally
kubectl port-forward -n hsm-secrets-operator-system service/hsm-secrets-operator-api 8090:8090

# Test health endpoint
curl http://localhost:8090/api/v1/health
```

## Configuration Options

### HSM Device Configuration

```yaml
spec:
  deviceType: PicoHSM  # or SmartCardHSM, Generic
  usb:
    vendorId: "20a0"
    productId: "4230"
  nodeSelector:
    hsm.j5t.io/enabled: "true"
  mirroring:
    policy: "ReadOnly"
    syncInterval: 300
    autoFailover: true
```

### Secret Configuration

```yaml
spec:
  hsmPath: "secrets/production/my-secret"
  secretName: "my-secret"
  autoSync: true
  syncInterval: 600
  secretType: Opaque  # or kubernetes.io/tls, etc.
```

## Security Considerations

### 1. RBAC
- Limit access to HSMSecret resources
- Use service accounts with minimal permissions
- Separate dev/staging/prod access

### 2. Network Security
- Use NetworkPolicies to restrict traffic
- Enable TLS for API communication
- Use private networks where possible

### 3. HSM Security
- Properly configure HSM authentication
- Regular security updates for OpenSC libraries
- Monitor HSM access and operations

### 4. Secret Management
- Use strong passwords and keys
- Implement secret rotation policies
- Monitor secret access and changes

## Monitoring and Alerting

### Key Metrics to Monitor
- HSM device connectivity
- Secret sync status and lag
- API response times and errors
- Operator pod health

### Sample Alerts
```yaml
# HSM Device Down
- alert: HSMDeviceDown
  expr: hsm_device_connected == 0
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "HSM device is disconnected"

# Secret Sync Failed
- alert: SecretSyncFailed
  expr: hsm_secret_sync_failed > 0
  for: 2m
  labels:
    severity: warning
  annotations:
    summary: "Secret synchronization failed"
```

## Troubleshooting

### Common Issues

1. **HSM Device Not Found**
   ```bash
   # Check USB devices
   lsusb
   
   # Check OpenSC
   pkcs11-tool --list-slots
   
   # Check node labels
   kubectl describe node <node-name>
   ```

2. **Secrets Not Syncing**
   ```bash
   # Check HSMSecret status
   kubectl describe hsmsecret <secret-name>
   
   # Check operator logs
   kubectl logs -n hsm-secrets-operator-system deployment/hsm-secrets-operator-controller-manager
   ```

3. **API Not Responding**
   ```bash
   # Check API service
   kubectl get service -n hsm-secrets-operator-system
   
   # Check API logs
   kubectl logs -n hsm-secrets-operator-system -l control-plane=controller-manager
   ```

## Backup and Recovery

### Backup HSM Configuration
```bash
# Export HSMDevice configurations
kubectl get hsmdevice -o yaml > hsm-devices-backup.yaml

# Export HSMSecret configurations
kubectl get hsmsecret --all-namespaces -o yaml > hsm-secrets-backup.yaml
```

### Recovery Process
1. Restore HSM devices and configure authentication
2. Deploy operator and CRDs
3. Apply HSMDevice configurations
4. Apply HSMSecret configurations
5. Verify secret synchronization