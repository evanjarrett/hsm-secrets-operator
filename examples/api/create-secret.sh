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

# Create the JSON payload
payload=$(cat <<EOF
{
  "label": "$SECRET_NAME",
  "id": $SECRET_ID,
  "format": "json",
  "description": "Secret created via API on $(date)",
  "tags": {
    "created_by": "api-script",
    "environment": "development",
    "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  },
  "data": {
    "api_key": "sk_test_$(openssl rand -hex 16)",
    "webhook_secret": "whsec_$(openssl rand -hex 20)",
    "database_url": "postgresql://user:$(openssl rand -hex 12)@localhost:5432/testdb",
    "redis_url": "redis://localhost:6379/0",
    "created_timestamp": "$(date +%s)"
  }
}
EOF
)

echo "📝 Request Payload:"
echo "$payload" | jq '.'
echo ""

# Make the API call
echo "📤 Sending create request..."
response=$(curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "$payload" \
  "$API_BASE_URL/api/v1/hsm/secrets")

echo "📥 Response:"
echo "$response" | jq '.'

# Check if the request was successful
success=$(echo "$response" | jq -r '.success')
if [ "$success" = "true" ]; then
    echo ""
    echo "✅ Secret created successfully!"
    
    # Extract created secret info
    label=$(echo "$response" | jq -r '.data.label')
    id=$(echo "$response" | jq -r '.data.id')
    path=$(echo "$response" | jq -r '.data.path')
    
    echo "   Label: $label"
    echo "   ID: $id"
    echo "   HSM Path: $path"
    
    echo ""
    echo "🔍 To retrieve this secret:"
    echo "   curl $API_BASE_URL/api/v1/hsm/secrets/$label"
    
    echo ""
    echo "📋 To check Kubernetes Secret:"
    echo "   kubectl get secret $label"
    echo "   kubectl describe secret $label"
    
else
    echo ""
    echo "❌ Failed to create secret!"
    error_code=$(echo "$response" | jq -r '.error.code // "unknown"')
    error_message=$(echo "$response" | jq -r '.error.message // "No error message"')
    echo "   Error Code: $error_code"
    echo "   Error Message: $error_message"
    exit 1
fi