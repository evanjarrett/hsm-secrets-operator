# HSM Secrets Operator Examples

This directory contains practical examples demonstrating how to use the HSM Secrets Operator in various scenarios.

## Directory Structure

- **[basic/](basic/)** - Basic usage examples for getting started
- **[advanced/](advanced/)** - Advanced configurations and use cases
- **[api/](api/)** - REST API usage examples
- **[high-availability/](high-availability/)** - High availability and mirroring setups

## Quick Start

1. **Install the Operator**
   ```bash
   # Install CRDs
   kubectl apply -f config/crd/bases/
   
   # Deploy the operator
   kubectl apply -f config/default/
   ```

2. **Create your first HSM Device**
   ```bash
   kubectl apply -f examples/basic/pico-hsm-device.yaml
   ```

3. **Create an HSM Secret**
   ```bash
   kubectl apply -f examples/basic/database-secret.yaml
   ```

4. **Use the REST API**
   ```bash
   # Check health
   curl http://localhost:8090/api/v1/health
   
   # Create a secret via API
   curl -X POST http://localhost:8090/api/v1/hsm/secrets \
     -H "Content-Type: application/json" \
     -d @examples/api/create-secret.json
   ```

## Prerequisites

- Kubernetes cluster (v1.20+)
- Pico HSM or compatible PKCS#11 device
- OpenSC libraries installed on nodes with HSM devices

## Common Use Cases

### 1. Database Credentials
Store and rotate database credentials securely using HSM hardware protection.
→ See [basic/database-secret.yaml](basic/database-secret.yaml)

### 2. TLS Certificates  
Manage TLS certificates with automatic sync to Kubernetes Secrets.
→ See [basic/tls-certificate.yaml](basic/tls-certificate.yaml)

### 3. API Keys
Store third-party API keys with hardware-based security.
→ See [basic/api-keys.yaml](basic/api-keys.yaml)

### 4. High Availability Setup
Configure cross-node mirroring for fault tolerance.
→ See [high-availability/](high-availability/)

### 5. Import Existing Secrets
Migrate existing Kubernetes Secrets to HSM storage.
→ See [api/import-from-k8s.sh](api/import-from-k8s.sh)

## Security Considerations

- HSM devices should be properly authenticated and configured
- Use RBAC to control access to HSMSecret resources
- Enable audit logging for secret operations
- Regular backup of HSM configurations (not the secrets themselves)

## Troubleshooting

Common issues and solutions:

1. **HSM Device Not Found**
   - Check USB connection and permissions
   - Verify OpenSC installation
   - Review HSMDevice status: `kubectl describe hsmdevice`

2. **Sync Failures**
   - Check HSM connectivity
   - Verify PKCS#11 library path
   - Review controller logs: `kubectl logs -n hsm-secrets-operator-system`

3. **API Server Issues**
   - Confirm API is enabled: `--enable-api=true`
   - Check port availability: `--api-port=8090`
   - Review API server logs

## Contributing

Found an issue or have a suggestion? Please open an issue or submit a pull request.

## Additional Resources

- [Operator Documentation](../README.md)
- [API Reference](../internal/api/types.go)
- [PKCS#11 Guide](https://www.opendnssec.org/softhsm/)