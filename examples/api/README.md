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
- Future: OAuth2, API keys, mTLS

## Examples

1. **[health-check.sh](health-check.sh)** - Check API and HSM health
2. **[create-secret.json](create-secret.json)** - Create a new secret via API
3. **[create-secret.sh](create-secret.sh)** - Script to create secrets
4. **[import-from-k8s.sh](import-from-k8s.sh)** - Import existing Kubernetes secrets
5. **[list-secrets.sh](list-secrets.sh)** - List all HSM secrets
6. **[update-secret.sh](update-secret.sh)** - Update existing secrets
7. **[bulk-operations.sh](bulk-operations.sh)** - Bulk secret operations

## Quick Start

1. **Start the API server** (if running locally):
   ```bash
   ./bin/manager --enable-api=true --api-port=8090
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
- Create secrets during development
- Import from existing sources
- Test secret rotation

### 2. CI/CD Integration
- Automated secret provisioning
- Environment-specific deployments
- Secret validation and testing

### 3. Secret Migration
- Import from Kubernetes Secrets
- Migrate from other secret stores
- Bulk operations for large environments

### 4. Monitoring and Operations
- Health monitoring
- Secret inventory management
- Troubleshooting sync issues

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