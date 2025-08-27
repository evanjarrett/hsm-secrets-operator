# High Availability Examples

This directory contains examples for setting up high availability configurations with HSM device mirroring.

## Overview

The HSM Secrets Operator supports high availability through cross-node device mirroring. When primary HSM devices become unavailable, the system automatically provides readonly access from mirror nodes.

## Examples

1. **[mirrored-hsm-device.yaml](mirrored-hsm-device.yaml)** - HSM device with mirroring enabled

> **Additional HA Examples:** See the [deployment/complete-setup.yaml](../deployment/complete-setup.yaml) for production HA configurations and the [advanced](../advanced/) directory for multi-environment setups.

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Node 1        │    │   Node 2        │    │   Node 3        │
│   (Primary)     │    │   (Mirror)      │    │   (Mirror)      │
├─────────────────┤    ├─────────────────┤    ├─────────────────┤
│ HSM Device      │    │ HSM Device      │    │ HSM Device      │
│ - Read/Write    │────│ - Read Only     │────│ - Read Only     │
│ - Sync Source   │    │ - Sync Target   │    │ - Sync Target   │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                        │                        │
         └────────────────────────┼────────────────────────┘
                                  │
                        ┌─────────▼─────────┐
                        │ Mirroring Manager │
                        │ - Sync Control    │
                        │ - Failover Logic  │
                        │ - Health Monitor  │
                        └───────────────────┘
```

## Key Features

### Automatic Mirroring
- Secrets are automatically synchronized from primary to mirror nodes
- Configurable sync intervals based on requirements
- Checksum validation ensures data integrity

### Readonly Fallback
- When primary HSM is unavailable, mirrors provide readonly access
- Applications continue to function with existing secrets
- No write operations allowed on mirror nodes

### Automatic Failover
- Failed primary nodes are automatically detected
- Healthy mirror nodes can be promoted to primary
- Configurable failover policies and thresholds

## Configuration Options

### Mirroring Policies

```yaml
mirroring:
  policy: "ReadOnly"           # None, ReadOnly, Active
  syncInterval: 300            # Sync every 5 minutes
  autoFailover: true           # Enable automatic failover
  primaryNode: "worker-1"      # Preferred primary node
  targetNodes:                 # Specific mirror nodes
    - "worker-2"
    - "worker-3"
```

### Device Roles

- **Primary**: Read/write access, source of truth
- **ReadOnly**: Synchronized copy, read-only access
- **Standby**: Available for failover promotion

### Health Monitoring

- Continuous health checks of HSM devices
- Network connectivity monitoring
- Sync status and lag detection
- Automatic alerts on failures

## Deployment Scenarios

### 1. Basic HA (2 Nodes)
- One primary, one mirror node
- Simple failover configuration
- Suitable for development/staging

### 2. Multi-Node HA (3+ Nodes)  
- One primary, multiple mirrors
- Enhanced redundancy
- Load distribution for read operations

### 3. Multi-Region HA
- Primary and mirrors across regions
- Network partition tolerance
- Disaster recovery capabilities

### 4. Active-Passive Clusters
- Dedicated HSM clusters
- Automated failover between clusters
- Geographic distribution

## Best Practices

### 1. Node Distribution
- Place HSM devices on different physical nodes
- Use node affinity to ensure proper distribution
- Consider network latency for sync operations

### 2. Sync Configuration
- Set appropriate sync intervals based on change frequency
- More frequent syncs for critical secrets
- Less frequent syncs for stable configurations

### 3. Monitoring
- Monitor sync lag and failures
- Set up alerts for health issues
- Track failover events and performance

### 4. Testing
- Regular failover testing
- Validate readonly access during failures  
- Test recovery procedures

## Troubleshooting

### Common Issues

1. **Sync Failures**
   - Check network connectivity between nodes
   - Verify HSM device health on all nodes
   - Review PKCS#11 library configuration

2. **Failover Not Working**
   - Confirm autoFailover is enabled
   - Check node health detection thresholds
   - Verify mirror nodes have healthy devices

3. **Read Performance Issues**
   - Review sync intervals and adjust as needed
   - Check network latency between nodes
   - Consider local caching strategies

### Monitoring Queries

```bash
# Check HSM device status
kubectl get hsmdevice -o wide

# Review mirroring status
kubectl describe hsmdevice hsm-primary

# Check secret sync status (multiple options)
kubectl hsm health                                    # via kubectl-hsm plugin
kubectl hsm list                                      # shows all secrets with status
kubectl get hsmsecret -o custom-columns=NAME:.metadata.name,STATUS:.status.syncStatus,LAST-SYNC:.status.lastSyncTime

# Monitor failover events
kubectl get events --field-selector reason=HSMFailover
```