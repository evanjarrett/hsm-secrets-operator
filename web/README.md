# HSM Secrets Manager Web UI

A simple web interface for managing Hardware Security Module (HSM) secrets through the HSM Secrets Operator.

## Features

- **📋 List Secrets**: View all secrets stored in your HSM
- **➕ Create Secrets**: Add new secrets with JSON key-value pairs
- **🔍 View Details**: Examine secret contents and metadata
- **🗑️ Delete Secrets**: Remove secrets from both HSM and Kubernetes
- **📊 Health Monitoring**: Check API and HSM status
- **🔄 Auto-refresh**: Automatically updates every 30 seconds

## Usage

### Starting the Web UI

The web UI is served by the HSM Secrets Operator manager on port 8090 by default:

1. **Using kubectl port-forward** (for local development):
   ```bash
   kubectl port-forward -n hsm-secrets-operator-system service/hsm-secrets-operator-manager-service 8090:8090
   ```

2. **Using ingress** (for production):
   Configure your ingress controller to route to the manager service on port 8090.

3. **Access the UI**:
   Open your browser to: `http://localhost:8090`

### Creating Secrets

1. Click **"➕ Create New Secret"**
2. Enter a **Secret Name** (this becomes the HSM path)
3. Add **Key-Value Pairs**:
   - Click the **➕** button to add a new key-value pair
   - Enter the key name (e.g., `api_key`, `database_password`)
   - Enter the corresponding value
   - Use **➖** to remove pairs you don't need
   - Add as many pairs as needed for your secret
4. Click **"Create Secret"**

**Key Naming Rules:**
- Must start with a letter
- Can contain letters, numbers, and underscores only
- Examples: `api_key`, `db_password`, `webhook_secret`

### Viewing Secrets

1. Click **"👁️ View"** next to any secret in the list
2. See the full JSON structure and metadata
3. Copy individual values as needed

### Managing Secrets

- **Refresh**: Click 🔄 to manually refresh the list
- **Delete**: Click 🗑️ and confirm to permanently remove a secret
- **Auto-sync**: The UI automatically refreshes every 30 seconds

## API Integration

The web UI communicates with the HSM Secrets Operator's REST API:

- **List Secrets**: `GET /api/v1/hsm/secrets`
- **Get Secret**: `GET /api/v1/hsm/secrets/{name}`
- **Create Secret**: `POST /api/v1/hsm/secrets/{name}`
- **Delete Secret**: `DELETE /api/v1/hsm/secrets/{name}`
- **Health Check**: `GET /api/v1/health`

## Security Considerations

- The web UI serves static files from the manager pod
- All API calls go through the manager, which proxies to HSM agent pods
- Secrets are displayed in the browser - use HTTPS in production
- Consider network policies to restrict access to the web interface

## Ingress Example

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hsm-secrets-ui
  namespace: hsm-secrets-operator-system
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  tls:
    - hosts:
        - hsm-secrets.example.com
      secretName: hsm-secrets-tls
  rules:
    - host: hsm-secrets.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: hsm-secrets-operator-manager-service
                port:
                  number: 8090
```

## Troubleshooting

### UI Not Loading
- Check that the manager pod is running: `kubectl get pods -n hsm-secrets-operator-system`
- Verify port-forward is active: `netstat -an | grep 8090`
- Check manager logs: `kubectl logs -n hsm-secrets-operator-system -l app.kubernetes.io/name=hsm-secrets-operator`

### API Errors
- Ensure HSM agents are running and healthy
- Check HSMPool status: `kubectl get hsmpool`
- Verify HSM devices are discovered: `kubectl get hsmdevice`

### No Secrets Visible
- Confirm secrets exist via CLI: `examples/api/list-secrets.sh`
- Check agent connectivity from manager pod
- Verify PKCS#11 configuration in HSMDevice CRDs

## Development

The web UI consists of:
- `index.html`: Main interface with responsive design
- `app.js`: JavaScript API client and UI logic
- Served via Gin router's static file handler

To modify the UI:
1. Edit files in the `web/` directory
2. Rebuild the manager: `make build`
3. Redeploy or restart the manager pod