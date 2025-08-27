#!/bin/bash

# Bulk operations for HSM secrets
# Automatically uses kubectl-hsm plugin if available, falls back to REST API
# Usage: ./bulk-operations.sh [operation] [config-file]

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}
OPERATION=${1:-"create"}
CONFIG_FILE=${2:-"bulk-secrets.json"}

# Function to create bulk secrets config if it doesn't exist
create_sample_config() {
    cat > "$CONFIG_FILE" <<EOF
{
  "secrets": [
    {
      "label": "app1-database",
      "id": 1001,
      "format": "json",
      "description": "Database credentials for app1",
      "tags": {
        "app": "app1",
        "environment": "production",
        "type": "database"
      },
      "data": {
        "database_url": "postgresql://app1:password123@db.example.com:5432/app1",
        "username": "app1",
        "password": "password123"
      }
    },
    {
      "label": "app2-api-keys",
      "id": 1002,
      "format": "json",
      "description": "External API keys for app2",
      "tags": {
        "app": "app2",
        "environment": "production",
        "type": "api-keys"
      },
      "data": {
        "stripe_key": "sk_live_example123",
        "sendgrid_key": "SG.example456",
        "aws_access_key": "AKIA1234567890"
      }
    },
    {
      "label": "shared-tls-cert",
      "id": 1003,
      "format": "text",
      "description": "Shared TLS certificate",
      "tags": {
        "type": "tls",
        "scope": "shared",
        "environment": "production"
      },
      "data": {
        "tls.crt": "-----BEGIN CERTIFICATE-----\\nMIIC...\\n-----END CERTIFICATE-----",
        "tls.key": "-----BEGIN PRIVATE KEY-----\\nMIIE...\\n-----END PRIVATE KEY-----"
      }
    }
  ]
}
EOF
    echo "📝 Created sample config file: $CONFIG_FILE"
}

# Function to create a single secret
create_secret() {
    local secret_data="$1"
    local label=$(echo "$secret_data" | jq -r '.label')
    
    echo "  Creating secret: $label"
    
    # Try using kubectl-hsm first if available, fallback to API
    if command -v kubectl >/dev/null && kubectl hsm --help >/dev/null 2>&1; then
        # Convert secret_data to create command format
        local temp_file=$(mktemp)
        echo "$secret_data" | jq '.data' > "$temp_file"
        
        if kubectl hsm create "$label" --from-file="$temp_file" >/dev/null 2>&1; then
            rm "$temp_file"
            echo "    ✅ Created successfully (kubectl-hsm)"
            return 0
        else
            rm "$temp_file"
            echo "    ⚠️  kubectl-hsm failed, trying API..."
        fi
    fi
    
    # Fallback to API
    response=$(curl -s -X POST \
      -H "Content-Type: application/json" \
      -d "$secret_data" \
      "$API_BASE_URL/api/v1/hsm/secrets")
    
    success=$(echo "$response" | jq -r '.success')
    if [ "$success" = "true" ]; then
        echo "    ✅ Created successfully (API)"
        return 0
    else
        error_message=$(echo "$response" | jq -r '.error.message // "Unknown error"')
        echo "    ❌ Failed: $error_message"
        return 1
    fi
}

# Function to delete a secret
delete_secret() {
    local label="$1"
    
    echo "  Deleting secret: $label"
    
    # Try using kubectl-hsm first if available, fallback to API
    if command -v kubectl >/dev/null && kubectl hsm --help >/dev/null 2>&1; then
        if kubectl hsm delete "$label" --force >/dev/null 2>&1; then
            echo "    ✅ Deleted successfully (kubectl-hsm)"
            return 0
        else
            echo "    ⚠️  kubectl-hsm failed, trying API..."
        fi
    fi
    
    # Fallback to API
    response=$(curl -s -X DELETE "$API_BASE_URL/api/v1/hsm/secrets/$label")
    
    success=$(echo "$response" | jq -r '.success')
    if [ "$success" = "true" ]; then
        echo "    ✅ Deleted successfully (API)"
        return 0
    else
        error_message=$(echo "$response" | jq -r '.error.message // "Unknown error"')
        echo "    ❌ Failed: $error_message"
        return 1
    fi
}

# Function to get secret info
get_secret() {
    local label="$1"
    
    # Try using kubectl-hsm first if available, fallback to API
    if command -v kubectl >/dev/null && kubectl hsm --help >/dev/null 2>&1; then
        if kubectl hsm get "$label" >/dev/null 2>&1; then
            echo "    ✅ $label (available via kubectl-hsm)"
            return 0
        fi
    fi
    
    # Fallback to API for detailed status
    response=$(curl -s "$API_BASE_URL/api/v1/hsm/secrets/$label")
    success=$(echo "$response" | jq -r '.success')
    
    if [ "$success" = "true" ]; then
        checksum=$(echo "$response" | jq -r '.data.metadata.checksum[0:8]')
        replicated=$(echo "$response" | jq -r '.data.metadata.is_replicated')
        echo "    ✅ $label (checksum: $checksum, replicated: $replicated)"
        return 0
    else
        echo "    ❌ $label (not found or error)"
        return 1
    fi
}

echo "🔄 HSM Secrets Bulk Operations"
echo "Operation: $OPERATION"
echo "Config File: $CONFIG_FILE"
echo "API Base URL: $API_BASE_URL"
echo ""

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "⚠️  Config file not found. Creating sample..."
    create_sample_config
    echo ""
fi

# Validate config file
if ! jq empty "$CONFIG_FILE" 2>/dev/null; then
    echo "❌ Invalid JSON in config file: $CONFIG_FILE"
    exit 1
fi

# Extract secret labels for operations
secret_labels=$(jq -r '.secrets[].label' "$CONFIG_FILE")
secret_count=$(echo "$secret_labels" | wc -l)

echo "📊 Found $secret_count secrets in config file"
echo ""

case "$OPERATION" in
    "create")
        echo "🔐 Creating secrets..."
        success_count=0
        failure_count=0
        
        # Process each secret
        while IFS= read -r secret_json; do
            if create_secret "$secret_json"; then
                ((success_count++))
            else
                ((failure_count++))
            fi
        done < <(jq -c '.secrets[]' "$CONFIG_FILE")
        
        echo ""
        echo "📈 Results:"
        echo "  ✅ Successful: $success_count"
        echo "  ❌ Failed: $failure_count"
        ;;
        
    "delete")
        echo "🗑️  Deleting secrets..."
        success_count=0
        failure_count=0
        
        # Delete each secret
        while IFS= read -r label; do
            if delete_secret "$label"; then
                ((success_count++))
            else
                ((failure_count++))
            fi
        done <<< "$secret_labels"
        
        echo ""
        echo "📈 Results:"
        echo "  ✅ Deleted: $success_count"
        echo "  ❌ Failed: $failure_count"
        ;;
        
    "status")
        echo "📊 Checking secret status..."
        available_count=0
        missing_count=0
        
        # Check each secret
        while IFS= read -r label; do
            if get_secret "$label"; then
                ((available_count++))
            else
                ((missing_count++))
            fi
        done <<< "$secret_labels"
        
        echo ""
        echo "📈 Results:"
        echo "  ✅ Available: $available_count"
        echo "  ❌ Missing: $missing_count"
        ;;
        
    "backup")
        echo "💾 Backing up secrets..."
        backup_file="secrets-backup-$(date +%Y%m%d-%H%M%S).json"
        
        echo '{"secrets": [' > "$backup_file"
        first=true
        
        while IFS= read -r label; do
            response=$(curl -s "$API_BASE_URL/api/v1/hsm/secrets/$label")
            success=$(echo "$response" | jq -r '.success')
            
            if [ "$success" = "true" ]; then
                if [ "$first" = true ]; then
                    first=false
                else
                    echo "," >> "$backup_file"
                fi
                echo "$response" | jq '.data' >> "$backup_file"
                echo "  ✅ Backed up: $label"
            else
                echo "  ❌ Failed to backup: $label"
            fi
        done <<< "$secret_labels"
        
        echo ']}' >> "$backup_file"
        echo ""
        echo "📁 Backup saved to: $backup_file"
        ;;
        
    *)
        echo "❌ Unknown operation: $OPERATION"
        echo ""
        echo "Available operations:"
        echo "  create  - Create all secrets from config file"
        echo "  delete  - Delete all secrets from config file"
        echo "  status  - Check status of all secrets"
        echo "  backup  - Backup all secrets to file"
        echo ""
        echo "Usage: $0 [operation] [config-file]"
        exit 1
        ;;
esac