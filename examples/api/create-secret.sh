#!/bin/bash

# Create HSM Secret via REST API
# Usage: ./create-secret.sh [secret-name] [secret-id]

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}
SECRET_NAME=${1:-"example-secret"}
SECRET_ID=${2:-"$(date +%s)"}  # Use timestamp as default ID

echo "🔐 Creating HSM Secret via API..."
echo "Secret Name: $SECRET_NAME"
echo "Secret ID: $SECRET_ID"
echo "API Base URL: $API_BASE_URL"
echo ""

# Create the JSON payload for agent API (path-based)
# The agent expects the path in the URL and data directly in the request body
payload=$(cat <<EOF
{
  "data": {
    "api_key": "sk_test_$(openssl rand -hex 16)",
    "webhook_secret": "whsec_$(openssl rand -hex 20)",
    "database_url": "postgresql://user:$(openssl rand -hex 12)@localhost:5432/testdb",
    "redis_url": "redis://localhost:6379/0",
    "created_timestamp": "$(date +%s)",
    "label": "$SECRET_NAME",
    "id": "$SECRET_ID",
    "description": "Secret created via API on $(date)",
    "created_by": "api-script",
    "environment": "development",
    "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  }
}
EOF
)

echo "📝 Request Payload:"
echo "$payload" | jq '.'
echo ""

# Create HSM path from secret name (just use the secret name as path)
HSM_PATH="$SECRET_NAME"

# Make the API call - using path-based endpoint
echo "📤 Sending create request to path: $HSM_PATH"
response=$(curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "$payload" \
  "$API_BASE_URL/api/v1/hsm/secrets/$HSM_PATH")

echo "📥 Response:"
echo "$response" | jq '.'

# Check if the request was successful
success=$(echo "$response" | jq -r '.success')
if [ "$success" = "true" ]; then
    echo ""
    echo "✅ Secret created successfully!"
    
    # Extract created secret info from agent response
    checksum=$(echo "$response" | jq -r '.data.checksum // "unknown"')
    path=$(echo "$response" | jq -r '.data.path // "unknown"')
    
    echo "   Secret Name: $SECRET_NAME"
    echo "   HSM Path: $HSM_PATH"
    echo "   Checksum: ${checksum:0:16}..."
    
    echo ""
    echo "🔍 To retrieve this secret:"
    echo "   curl $API_BASE_URL/api/v1/hsm/secrets/$HSM_PATH"
    
    echo ""
    echo "📋 To list all secrets:"
    echo "   curl $API_BASE_URL/api/v1/hsm/secrets"
    
    echo ""
    echo "🗑️  To delete this secret:"
    echo "   curl -X DELETE $API_BASE_URL/api/v1/hsm/secrets/$HSM_PATH"
    
else
    echo ""
    echo "❌ Failed to create secret!"
    error_code=$(echo "$response" | jq -r '.error.code // "unknown"')
    error_message=$(echo "$response" | jq -r '.error.message // "No error message"')
    echo "   Error Code: $error_code"
    echo "   Error Message: $error_message"
    exit 1
fi