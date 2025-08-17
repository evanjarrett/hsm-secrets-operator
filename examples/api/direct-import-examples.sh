#!/bin/bash

# Direct API import examples for HSM secrets
# Demonstrates various import patterns

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}

echo "🚀 Direct API Import Examples"
echo "=============================="

# Example 1: Import from environment variables
echo ""
echo "📋 Example 1: Import from Environment Variables"
curl -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
  -H "Content-Type: application/json" \
  -d '{
    "label": "env-config",
    "id": 3001,
    "format": "json",
    "description": "Application configuration from environment",
    "tags": {
      "source": "environment",
      "type": "config"
    },
    "data": {
      "NODE_ENV": "'${NODE_ENV:-development}'",
      "LOG_LEVEL": "'${LOG_LEVEL:-info}'",
      "PORT": "'${PORT:-3000}'"
    }
  }'

echo ""
echo ""

# Example 2: Import TLS certificates
echo "📋 Example 2: Import TLS Certificate Bundle"
curl -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
  -H "Content-Type: application/json" \
  -d '{
    "label": "app-tls-bundle",
    "id": 3002,
    "format": "text",
    "description": "Application TLS certificate bundle",
    "tags": {
      "type": "tls",
      "app": "web-server"
    },
    "data": {
      "server.crt": "-----BEGIN CERTIFICATE-----\nMIIDXTCCAkWgAwIBAgIJAK...\n-----END CERTIFICATE-----",
      "server.key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG...\n-----END PRIVATE KEY-----",
      "ca-bundle.crt": "-----BEGIN CERTIFICATE-----\nMIIDSjCCAjKgAwIBAgIQ...\n-----END CERTIFICATE-----"
    }
  }'

echo ""
echo ""

# Example 3: Import database connection strings
echo "📋 Example 3: Import Database Connections"
for db in primary replica analytics; do
  echo "Importing $db database connection..."
  curl -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
    -H "Content-Type: application/json" \
    -d '{
      "label": "db-'$db'",
      "id": '$((3003 + $(echo $db | wc -c)))',
      "format": "json",
      "description": "'$db' database connection details",
      "tags": {
        "type": "database",
        "role": "'$db'"
      },
      "data": {
        "host": "'$db'.db.internal",
        "port": "5432",
        "database": "app_'$db'",
        "username": "app_user",
        "password": "secure_'$db'_password",
        "connection_string": "postgresql://app_user:secure_'$db'_password@'$db'.db.internal:5432/app_'$db'"
      }
    }'
  echo ""
done

echo ""

# Example 4: Import API keys from CSV-like data
echo "📋 Example 4: Import Multiple API Keys"
api_services=("stripe" "sendgrid" "aws" "github")
api_keys=("sk_live_example123" "SG.example456" "AKIA1234567890" "ghp_example789")

for i in "${!api_services[@]}"; do
  service="${api_services[$i]}"
  key="${api_keys[$i]}"
  
  echo "Importing $service API key..."
  curl -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
    -H "Content-Type: application/json" \
    -d '{
      "label": "'$service'-api-key",
      "id": '$((3100 + $i))',
      "format": "text",
      "description": "'$service' API authentication key",
      "tags": {
        "type": "api-key",
        "service": "'$service'"
      },
      "data": {
        "api_key": "'$key'"
      }
    }'
  echo ""
done

echo ""

# Example 5: Import from file content
echo "📋 Example 5: Import from File (if exists)"
if [ -f "/tmp/secret-file.txt" ]; then
  file_content=$(cat /tmp/secret-file.txt | base64 -w 0)
  
  curl -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
    -H "Content-Type: application/json" \
    -d '{
      "label": "file-based-secret",
      "id": 3200,
      "format": "binary",
      "description": "Secret imported from file",
      "tags": {
        "source": "file",
        "encoding": "base64"
      },
      "data": {
        "content": "'$file_content'"
      }
    }'
else
  echo "⚠️  /tmp/secret-file.txt not found, skipping file import example"
fi

echo ""
echo ""

# Example 6: Batch import with error handling
echo "📋 Example 6: Batch Import with Error Handling"
secrets_to_import='[
  {
    "label": "batch-secret-1",
    "id": 3301,
    "format": "json",
    "data": {"key": "value1"}
  },
  {
    "label": "batch-secret-2", 
    "id": 3302,
    "format": "json",
    "data": {"key": "value2"}
  },
  {
    "label": "batch-secret-3",
    "id": 3303,
    "format": "json", 
    "data": {"key": "value3"}
  }
]'

echo "$secrets_to_import" | jq -c '.[]' | while IFS= read -r secret; do
  label=$(echo "$secret" | jq -r '.label')
  echo "Importing: $label"
  
  response=$(curl -s -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
    -H "Content-Type: application/json" \
    -d "$secret")
  
  success=$(echo "$response" | jq -r '.success')
  if [ "$success" = "true" ]; then
    echo "  ✅ Success"
  else
    error_msg=$(echo "$response" | jq -r '.error.message // "Unknown error"')
    echo "  ❌ Failed: $error_msg"
  fi
done

echo ""
echo ""

# Example 7: Import with validation
echo "📋 Example 7: Import with Pre-validation"
validate_and_import() {
  local label="$1"
  local secret_data="$2"
  
  # Check if secret already exists
  existing=$(curl -s "$API_BASE_URL/api/v1/hsm/secrets/$label")
  exists=$(echo "$existing" | jq -r '.success')
  
  if [ "$exists" = "true" ]; then
    echo "⚠️  Secret '$label' already exists, skipping..."
    return 1
  fi
  
  # Validate JSON structure
  if ! echo "$secret_data" | jq empty 2>/dev/null; then
    echo "❌ Invalid JSON for secret '$label'"
    return 1
  fi
  
  # Import the secret
  echo "Creating new secret: $label"
  response=$(curl -s -X POST "$API_BASE_URL/api/v1/hsm/secrets" \
    -H "Content-Type: application/json" \
    -d "$secret_data")
  
  success=$(echo "$response" | jq -r '.success')
  if [ "$success" = "true" ]; then
    echo "  ✅ Imported successfully"
    return 0
  else
    error_msg=$(echo "$response" | jq -r '.error.message // "Unknown error"')
    echo "  ❌ Import failed: $error_msg"
    return 1
  fi
}

# Test the validation function
validate_and_import "validated-secret" '{
  "label": "validated-secret",
  "id": 3400,
  "format": "json",
  "description": "A secret imported with validation",
  "data": {
    "validated": true,
    "timestamp": "'$(date -Iseconds)'"
  }
}'

echo ""
echo "🎉 All import examples completed!"
echo ""
echo "To verify imports:"
echo "  curl $API_BASE_URL/api/v1/hsm/secrets"