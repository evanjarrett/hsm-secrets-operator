# Advanced Examples

This directory contains advanced configuration examples for complex use cases.

## Examples Overview

1. **[custom-discovery.yaml](custom-discovery.yaml)** - Custom USB device discovery
2. **[multi-environment.yaml](multi-environment.yaml)** - Multi-environment secret management
3. **[secret-rotation.yaml](secret-rotation.yaml)** - Automated secret rotation
4. **[monitoring.yaml](monitoring.yaml)** - Prometheus monitoring setup

## Advanced Use Cases

### Custom Device Discovery

Configure HSM device discovery for non-standard devices or custom paths:

```yaml
# Custom USB device
usb:
  vendorId: "1234"
  productId: "5678"
  serialNumber: "CUSTOM-HSM-001"

# Custom device path
devicePath:
  path: "/dev/custom-hsm*"
  permissions: "0600"
```

### Multi-Environment Management

Organize secrets across different environments with proper isolation:

- Development secrets in `dev` namespace
- Staging secrets in `staging` namespace  
- Production secrets in `production` namespace
- Shared secrets with proper RBAC controls

### Secret Rotation

Implement automated secret rotation workflows:

- Database password rotation with zero downtime
- API key rotation with gradual rollout
- Certificate renewal with automatic deployment

### Monitoring and Alerting

Set up comprehensive monitoring:

- HSM device health monitoring
- Secret sync status tracking
- Performance metrics collection
- Alert rules for failures

## Advanced Configuration

### Node Affinity

Deploy HSM devices only on specific nodes:

```yaml
nodeSelector:
  hsm.j5t.io/hardware: "nitrokey"
  kubernetes.io/arch: "amd64"
  node-role.kubernetes.io/worker: ""
```

### Resource Limits

Configure resource limits for the operator:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

### Security Contexts

Run with minimal privileges:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  fsGroup: 2000
  capabilities:
    drop:
    - ALL
```

## Best Practices

### 1. Namespace Isolation
- Use separate namespaces for different environments
- Apply NetworkPolicies to restrict access
- Use RBAC to control HSMSecret access

### 2. Secret Lifecycle
- Plan for secret rotation and expiration
- Monitor secret age and usage
- Implement backup and recovery procedures

### 3. Monitoring
- Track HSM device health
- Monitor sync failures and delays
- Set up alerting for critical issues

### 4. Security
- Use least privilege access principles
- Regular security audits of HSM configurations
- Proper key management procedures