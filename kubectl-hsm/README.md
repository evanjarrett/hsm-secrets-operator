# kubectl-hsm Plugin

A kubectl plugin that provides a Kubernetes-native command-line interface for Hardware Security Module (HSM) secret management.

## Overview

The `kubectl-hsm` plugin integrates with the [HSM Secrets Operator](https://github.com/evanjarrett/hsm-secrets-operator) to provide secure secret storage using hardware-based security modules while maintaining seamless integration with Kubernetes workflows.

## Features

- **Kubernetes-native**: Works seamlessly with kubectl and respects namespace context
- **Secure**: All secrets stored in HSM hardware for maximum security
- **Interactive**: Support for secure password input and interactive secret creation
- **Flexible**: Multiple input methods (literals, files, JSON import, interactive prompts)
- **Simple naming**: Uses filenames (without extension) as secret keys - just add `.txt` for clean domain names
- **Collision detection**: Warns when file imports would overwrite existing keys
- **Bulk import**: JSON file support for migrating secrets from other systems
- **Shell completion**: Tab completion for commands, flags, and secret names (bash/zsh/fish/powershell)
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

## Shell Completion

The plugin supports bash, zsh, fish, and PowerShell completion for commands, flags, and secret names.

### Bash Completion

```bash
# Load completions in your current shell session
source <(kubectl hsm completion bash)

# Install completions for all sessions
## Linux:
kubectl hsm completion bash > /etc/bash_completion.d/kubectl-hsm

## macOS:
kubectl hsm completion bash > $(brew --prefix)/etc/bash_completion.d/kubectl-hsm

# Restart your shell
```

### Zsh Completion

```bash
# Load completions in your current shell session
source <(kubectl hsm completion zsh)

# Install completions for all sessions
## Using zsh completion system:
kubectl hsm completion zsh > "${fpath[1]}/_kubectl-hsm"

## Using oh-my-zsh:
mkdir -p ~/.oh-my-zsh/completions
kubectl hsm completion zsh > ~/.oh-my-zsh/completions/_kubectl-hsm

# Restart your shell
```

### Features

Completion provides:
- **Command completion**: `kubectl hsm <TAB>` shows available commands
- **Secret name completion**: `kubectl hsm get <TAB>` shows your actual secrets
- **Flag completion**: `kubectl hsm get --<TAB>` shows available flags  
- **Value completion**: `kubectl hsm get secret -o <TAB>` shows `text`, `json`, `yaml`

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

# Load secret values from files (explicit key names)
kubectl hsm create tls-cert \
  --from-file cert=server.crt \
  --from-file key=server.key

# Load files using filename as key (extensions removed)
kubectl hsm create dns-config \
  --from-file ./example.com.txt \
  --from-file ./j5t.io.txt \
  --from-file ./config.txt

# Load from JSON file (bulk import)
kubectl hsm create my-secrets --from-json-file=secrets.json

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

### File Loading Options

The plugin supports several ways to load secrets from files:

#### 1. Explicit Key Names (Recommended)
```bash
# Specify both key name and file path
kubectl hsm create tls-cert \
  --from-file cert=./server.crt \
  --from-file key=./private.key
```

#### 2. Filename as Key
```bash  
# Uses filename (without extension) as key name
kubectl hsm create config --from-file ./database.conf
# Creates key "database" with contents of database.conf

# Extensions are removed from filename to create key
kubectl hsm create dns-zones \
  --from-file ./example.com.txt \
  --from-file ./j5t.io.txt
# Creates keys "example.com" and "j5t.io" (extension .txt removed)
```

#### 3. JSON Import (New!)
```bash
# Import from structured JSON file
kubectl hsm create bulk-secrets --from-json-file=import.json
```

**JSON format:**
```json
{
  "name": "secret-name-in-file", 
  "secrets": [
    {"key": "api_key", "value": "sk_123"},
    {"key": "endpoint", "value": "https://api.example.com"},
    {"key": "config.yaml", "value": "server:\n  port: 8080"}
  ]
}
```

#### Key Collision Detection
When using multiple `--from-file` arguments, the plugin warns about key conflicts:
```bash
kubectl hsm create test --from-file ./config.txt --from-file ./backup/config.txt
# Warning: Key 'config' already exists, overwriting with value from './backup/config.txt'
```

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
# Method 1: Explicit key names (recommended)
kubectl hsm create tls-server \
  --from-file tls.crt=server.crt \
  --from-file tls.key=server.key \
  --from-file ca.crt=ca.crt

# Method 2: Use filenames as keys (extension removed)
kubectl hsm create tls-domains \
  --from-file ./example.com.crt \
  --from-file ./example.com.key \
  --from-file ./api.example.com.crt
# Creates keys: "example.com", "example.com", "api.example.com" (extensions removed)

# Check certificate info
kubectl hsm get tls-server
```

### DNS Configuration (New Use Case)

Perfect for managing DNS zone files with domain names as keys:

```bash
# Load DNS zone files - use .txt extension for clean keys
kubectl hsm create dns-zones \
  --from-file ./j5t.io.txt \
  --from-file ./example.com.txt \
  --from-file ./internal.net.txt

# Results in keys: "j5t.io", "example.com", "internal.net"
kubectl hsm get dns-zones
```

### Bulk Import from JSON

```bash  
# Import multiple secrets from JSON (useful for migrations)
kubectl hsm create imported-secrets --from-json-file=bitwarden-export.json

# JSON structure matches your export format:
# {
#   "name": "dns-config",
#   "secrets": [
#     {"key": "j5t.io", "value": "@ IN SOA ns.j5t.io..."},
#     {"key": "jarrett.net", "value": "@ IN SOA ns.jarrett.net..."} 
#   ]
# }
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