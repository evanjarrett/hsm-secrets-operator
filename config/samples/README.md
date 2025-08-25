# HSMSecret Cross-Namespace Support

This directory contains sample HSMSecret manifests demonstrating cross-namespace functionality.

## ParentRef-Based Operator Association

When multiple HSM operator instances are deployed in a cluster, HSMSecrets use `parentRef` to specify which operator should handle them:

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: my-secret
  namespace: production
spec:
  parentRef:
    name: controller-manager
    namespace: hsm-operator-system
  # ... rest of spec
```

## Behavior

- **With parentRef**: Only the operator with matching name and namespace will handle the HSMSecret
- **Without parentRef**: HSMSecret is ignored by all operators (explicit association required)

## Architecture

- **HSMSecrets**: Can be created in any namespace
- **Kubernetes Secrets**: Created in the same namespace as their HSMSecret
- **Operator Infrastructure**: HSMDevices, HSMPools, agents remain in the operator's namespace
- **RBAC**: ClusterRole provides cluster-wide permissions

## Helm Integration

When deploying via Helm, the `parentRef` is automatically added to HSMSecrets:

```yaml
# In Helm values.yaml
hsmsecret:
  enabled: true
  secrets:
    - name: "database-credentials"
      namespace: "production"
      secretName: "db-secrets"
      syncInterval: 300
      autoSync: true
    - name: "api-keys"
      namespace: "development"
      secretName: "third-party-keys"
      syncInterval: 60
```

This creates HSMSecrets with automatically generated `parentRef`:

```yaml
apiVersion: hsm.j5t.io/v1alpha1
kind: HSMSecret
metadata:
  name: database-credentials
  namespace: production
spec:
  parentRef:
    name: my-release-hsm-secrets-operator-controller-manager
    namespace: my-operator-namespace
  secretName: db-secrets
  syncInterval: 300
  autoSync: true
```

**Benefits**:
- No manual parentRef configuration needed
- Automatic association with the deploying Helm release
- Multi-tenant support for multiple operator deployments
- Cross-namespace secret management with explicit operator ownership