# Basic Examples

This directory contains basic usage examples to get you started with the HSM Secrets Operator.

## Examples Overview

1. **[pico-hsm-device.yaml](pico-hsm-device.yaml)** - HSM device discovery configuration
2. **[database-secret.yaml](database-secret.yaml)** - Database credentials management
3. **[tls-certificate.yaml](tls-certificate.yaml)** - TLS certificate storage
4. **[api-keys.yaml](api-keys.yaml)** - Third-party API key management

## Getting Started

### Step 1: Configure HSM Device

First, create an HSMDevice resource to discover and configure your Pico HSM:

```bash
kubectl apply -f pico-hsm-device.yaml
```

Check the device status:
```bash
kubectl get hsmdevice pico-hsm -o yaml
kubectl describe hsmdevice pico-hsm
```

### Step 2: Create Your First Secret

Create a database secret stored on the HSM:

```bash
kubectl apply -f database-secret.yaml
```

Verify the secret was created:
```bash
kubectl get hsmsecret database-credentials
kubectl get secret database-credentials
```

### Step 3: Use the Secret in Your Application

The operator automatically creates a Kubernetes Secret that your applications can use:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  template:
    spec:
      containers:
      - name: app
        image: myapp:latest
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: database-credentials
              key: database_url
        - name: DB_USERNAME
          valueFrom:
            secretKeyRef:
              name: database-credentials
              key: username
```

## Key Concepts

### HSMDevice
Represents a physical HSM device and handles:
- USB device discovery
- PKCS#11 library configuration
- Device health monitoring

### HSMSecret
Represents a secret stored on the HSM and manages:
- Bidirectional sync with Kubernetes Secrets
- Data integrity with checksums
- Automatic updates when HSM data changes

### Sync Process
1. HSMSecret reads data from HSM using PKCS#11
2. Creates/updates corresponding Kubernetes Secret
3. Monitors for changes and keeps both in sync
4. Provides status and health information

## Common Patterns

### Environment-Specific Secrets
Use namespaces to separate secrets by environment:

```bash
# Production
kubectl apply -f database-secret.yaml -n production

# Staging  
kubectl apply -f database-secret.yaml -n staging
```

### Secret Rotation
Update secrets directly on the HSM, and they'll automatically sync:

```bash
# The operator detects HSM changes and updates Kubernetes Secrets
# No manual intervention required
```

### Multiple Applications
Share the same HSM secret across multiple applications:

```yaml
# App 1
apiVersion: v1
kind: Secret
metadata:
  name: app1-db-secret
data:
  url: <from-hsm-secret>

# App 2  
apiVersion: v1
kind: Secret
metadata:
  name: app2-db-secret
data:
  url: <from-hsm-secret>
```