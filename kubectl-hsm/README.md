# kubectl-hsm Plugin

A kubectl plugin that provides a Kubernetes-native command-line interface for Hardware Security Module (HSM) secret management.

## Overview

The `kubectl-hsm` plugin integrates with the [HSM Secrets Operator](https://github.com/evanjarrett/hsm-secrets-operator) to provide secure secret storage using hardware-based security modules while maintaining seamless integration with Kubernetes workflows.

## Features

- **Kubernetes-native**: Works seamlessly with kubectl and respects namespace context
- **Secure**: All secrets stored in HSM hardware for maximum security
- **Interactive**: Support for secure password input and interactive secret creation
- **Flexible**: Multiple input methods (literals, files, interactive prompts)
- **Cross-platform**: Supports Linux, macOS, and Windows

## Installation

### Quick Install (Recommended)

```bash
curl -fsSL https://github.com/evanjarrett/hsm-secrets-operator/releases/latest/download/install.sh | bash
```

### Manual Installation

1. Download the binary for your platform from the [releases page](https://github.com/evanjarrett/hsm-secrets-operator/releases)
2. Rename it to `kubectl-hsm`
3. Make it executable: `chmod +x kubectl-hsm`
4. Move it to a directory in your PATH (e.g., `/usr/local/bin/`)

### Build from Source

```bash
git clone https://github.com/evanjarrett/hsm-secrets-operator.git
cd hsm-secrets-operator/cmd/kubectl-hsm
make build
make install
```

## Prerequisites

- kubectl installed and configured
- HSM Secrets Operator deployed in your cluster
- Access to the operator's API service

## Usage

### Basic Commands

```bash
# Check plugin version
kubectl hsm version

# Check operator health
kubectl hsm health

# List all secrets
kubectl hsm list

# Create a secret interactively (recommended for sensitive data)
kubectl hsm create database-creds --interactive

# Create a secret with literal values
kubectl hsm create api-config \
  --from-literal api_key=sk_test_123 \
  --from-literal endpoint=https://api.example.com

# Load secret values from files
kubectl hsm create tls-cert \
  --from-file cert=server.crt \
  --from-file key=server.key

# Get a secret (shows metadata, not values)
kubectl hsm get database-creds

# Get a specific key value
kubectl hsm get database-creds --key password

# Delete a secret (with confirmation)
kubectl hsm delete old-credentials

# Force delete without confirmation
kubectl hsm delete old-credentials --force
```

### Namespace Handling

The plugin respects your current kubectl namespace context:

```bash
# Use current namespace context (set by kubens, kubectl config, etc.)
kubectl hsm list

# Override namespace for a single command
kubectl hsm list -n hsm-secrets-operator-system

# Switch namespace context (affects all kubectl commands)
kubens hsm-secrets-operator-system
kubectl hsm list
```

### Output Formats

```bash
# Human-readable output (default)
kubectl hsm get my-secret

# JSON output
kubectl hsm get my-secret -o json

# YAML output
kubectl hsm get my-secret -o yaml
```

### Interactive Mode

Interactive mode is recommended for creating secrets with sensitive data:

```bash
kubectl hsm create database-creds --interactive
```

This will prompt you for each key-value pair. Fields that look like passwords (containing "password", "secret", "token", or "key") will hide input for security.

## How It Works

1. **Service Discovery**: The plugin automatically discovers the HSM operator API service in your current namespace
2. **Port Forwarding**: Creates a secure port-forward connection to the operator
3. **API Proxy**: Routes commands through the operator's REST API
4. **HSM Integration**: The operator handles the actual HSM hardware communication

## Error Handling

The plugin provides helpful error messages and suggestions:

```bash
# If operator not found
❌ HSM secrets operator service not found in namespace 'default'

Please check:
  - Is the operator installed? Try: kubectl get deploy -n default
  - Are you in the correct namespace? Try: kubens <operator-namespace>

# If connection fails
❌ Failed to connect to HSM operator: connection refused

Please check:
  - Operator pods are running: kubectl get pods -l app.kubernetes.io/name=hsm-secrets-operator
  - Service is available: kubectl get svc hsm-secrets-operator-api
```

## Security Considerations

- **No Secret Values in Transit**: The plugin never logs or exposes secret values
- **Secure Communication**: All communication uses port-forwarded connections
- **HSM Storage**: Secrets are stored in HSM hardware, not Kubernetes etcd
- **Interactive Input**: Passwords are hidden during interactive input
- **Confirmation Required**: Destructive operations require explicit confirmation

## Configuration

The plugin uses standard kubectl configuration:

- **Kubeconfig**: Reads from `~/.kube/config` or `$KUBECONFIG`
- **Current Context**: Respects the current kubectl context
- **Namespace**: Uses current namespace or explicit `-n` flag

## Examples

### Database Credentials

```bash
# Create database credentials interactively
kubectl hsm create database-creds --interactive
# Prompts for: username, password, host, port, database

# Retrieve for use in deployment
kubectl hsm get database-creds --key username
kubectl hsm get database-creds --key password -o json | jq -r '.password'
```

### API Keys and Tokens

```bash
# Create API configuration
kubectl hsm create stripe-config \
  --from-literal publishable_key=pk_live_123 \
  --from-literal secret_key=sk_live_456 \
  --from-literal webhook_secret=whsec_789

# View configuration (metadata only)
kubectl hsm get stripe-config
```

### TLS Certificates

```bash
# Load certificate files
kubectl hsm create tls-server \
  --from-file tls.crt=server.crt \
  --from-file tls.key=server.key \
  --from-file ca.crt=ca.crt

# Check certificate info
kubectl hsm get tls-server
```

## Troubleshooting

### Plugin Not Found

If kubectl can't find the plugin:

1. Ensure the binary is named `kubectl-hsm`
2. Verify it's in your PATH: `which kubectl-hsm`
3. Check permissions: `ls -la $(which kubectl-hsm)`

### Connection Issues

If you can't connect to the operator:

1. Check if the operator is running: `kubectl get pods -n hsm-secrets-operator-system`
2. Verify the service exists: `kubectl get svc hsm-secrets-operator-api -n hsm-secrets-operator-system`
3. Test direct connection: `kubectl port-forward -n hsm-secrets-operator-system svc/hsm-secrets-operator-api 8090:8090`

### Permission Errors

If you get permission errors:

1. Verify your kubectl access: `kubectl auth can-i get services`
2. Check if you're in the right namespace: `kubectl config get-contexts`
3. Ensure the operator is accessible from your namespace

## Contributing

Contributions are welcome! Please see the main [repository](https://github.com/evanjarrett/hsm-secrets-operator) for contribution guidelines.

## License

This project is licensed under the Apache License, Version 2.0. See the [LICENSE](../../LICENSE) file for details.