# REST API Examples

This directory contains examples for using the HSM Secrets Operator REST API.

## API Overview

The operator provides a REST API server that runs on port 8090 (configurable). The API allows you to:

- Create, read, update, and delete secrets stored on HSM devices
- Import secrets from external sources (Kubernetes, Vault, etc.)
- Monitor HSM device health and status
- List and manage all HSM-backed secrets

## Base URL

When running locally: `http://localhost:8090`  
When deployed in cluster: `http://hsm-secrets-operator-api:8090`

## Authentication

The API currently supports:
- No authentication (development/testing)
- Kubernetes ServiceAccount tokens (when deployed in cluster)
- Future: OAuth2, API keys

## kubectl-hsm Plugin (Recommended)

The easiest way to interact with HSM secrets is through the `kubectl-hsm` plugin. This provides a native kubectl experience while automatically handling port forwarding and API communication.

### Quick Start with kubectl-hsm

1. **Install the plugin**:
   ```bash
   cd kubectl-hsm && make install
   ```

2. **Check health**:
   ```bash
   kubectl hsm health
   ```

3. **Create a secret**:
   ```bash
   kubectl hsm create my-secret --from-literal=password=secret123
   ```

4. **List secrets**:
   ```bash
   kubectl hsm list
   ```

5. **Get a secret**:
   ```bash
   kubectl hsm get my-secret
   ```

See the [kubectl-hsm documentation](../../kubectl-hsm/README.md) for full usage.

## REST API Examples

For advanced use cases or automation that requires direct API access, the following scripts demonstrate REST API usage:

1. **[import-from-k8s.sh](import-from-k8s.sh)** - Import existing Kubernetes secrets
2. **[bulk-operations.sh](bulk-operations.sh)** - Bulk secret operations (auto-detects kubectl-hsm)
3. **[advanced-bulk-import.sh](advanced-bulk-import.sh)** - Advanced import with validation and rollback
4. **[create-secret.json](create-secret.json)** - Sample secret data structure
5. **[production-import.json](production-import.json)** - Production-ready import configuration

## Quick Start with REST API

1. **Port forward to API server**:
   ```bash
   kubectl port-forward -n hsm-secrets-operator-system svc/hsm-secrets-operator-api 8090:8090
   ```

2. **Check health**:
   ```bash
   curl http://localhost:8090/api/v1/health
   ```

3. **Create a secret**:
   ```bash
   curl -X POST http://localhost:8090/api/v1/hsm/secrets \
     -H "Content-Type: application/json" \
     -d @create-secret.json
   ```

4. **List secrets**:
   ```bash
   curl http://localhost:8090/api/v1/hsm/secrets
   ```

## Response Format

All API responses follow this format:

```json
{
  "success": true,
  "message": "Operation completed successfully",
  "data": {
    // Response data here
  },
  "error": null
}
```

Error responses:
```json
{
  "success": false,
  "message": "",
  "data": null,
  "error": {
    "code": "validation_failed",
    "message": "Request validation failed",
    "details": {
      "field": "validation error details"
    }
  }
}
```

## Common Use Cases

### 1. Development Workflow
- **kubectl-hsm**: Interactive secret management during development
- **REST API**: Automated testing and CI/CD integration

### 2. CI/CD Integration
- **kubectl-hsm**: Simple command-line operations in pipelines
- **REST API**: Complex automation and bulk operations

### 3. Secret Migration
- **bulk-operations.sh**: Mass import/export operations
- **advanced-bulk-import.sh**: Production migrations with validation
- **import-from-k8s.sh**: Migrate existing Kubernetes secrets

### 4. Monitoring and Operations
- **kubectl-hsm health**: Quick health checks
- **REST API**: Detailed monitoring and troubleshooting

## Error Handling

Common error codes and solutions:

- `hsm_unavailable`: HSM device not connected
- `validation_failed`: Invalid request data
- `secret_not_found`: Secret doesn't exist
- `hsm_read_error`: HSM communication failure
- `hsm_write_error`: HSM storage failure

## Security Best Practices

1. **Network Security**
   - Use TLS in production
   - Restrict API access with NetworkPolicies
   - Use VPN or private networks

2. **Authentication**
   - Enable authentication in production
   - Use strong API keys or tokens
   - Implement proper RBAC

3. **Data Validation**
   - Validate all input data
   - Sanitize sensitive information in logs
   - Use structured logging

4. **Monitoring**
   - Monitor API access and usage
   - Track secret operations
   - Set up alerts for failures