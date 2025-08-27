#!/bin/bash

# Import existing Kubernetes Secret to HSM via REST API
# Usage: ./import-from-k8s.sh [secret-name] [namespace] [target-label] [target-id]

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}
SOURCE_SECRET=${1:-""}
SOURCE_NAMESPACE=${2:-"default"}
TARGET_LABEL=${3:-""}
TARGET_ID=${4:-"$(date +%s)"}

if [ -z "$SOURCE_SECRET" ]; then
    echo "Usage: $0 <source-secret-name> [namespace] [target-label] [target-id]"
    echo ""
    echo "Available secrets in namespace '$SOURCE_NAMESPACE':"
    kubectl get secrets -n "$SOURCE_NAMESPACE" --field-selector type=Opaque -o name | sed 's/secret\///'
    exit 1
fi

if [ -z "$TARGET_LABEL" ]; then
    TARGET_LABEL="$SOURCE_SECRET-hsm"
fi

echo "📦 Importing Kubernetes Secret to HSM..."
echo "Source Secret: $SOURCE_SECRET"
echo "Source Namespace: $SOURCE_NAMESPACE"
echo "Target Label: $TARGET_LABEL"
echo "Target ID: $TARGET_ID"
echo "API Base URL: $API_BASE_URL"
echo ""

# Check if source secret exists
echo "🔍 Checking source secret..."
if ! kubectl get secret "$SOURCE_SECRET" -n "$SOURCE_NAMESPACE" >/dev/null 2>&1; then
    echo "❌ Source secret '$SOURCE_SECRET' not found in namespace '$SOURCE_NAMESPACE'"
    exit 1
fi

# Show source secret info
echo "📋 Source secret info:"
kubectl describe secret "$SOURCE_SECRET" -n "$SOURCE_NAMESPACE"
echo ""

# Create the import request payload
payload=$(cat <<EOF
{
  "source": "kubernetes",
  "secret_name": "$SOURCE_SECRET",
  "secret_namespace": "$SOURCE_NAMESPACE",
  "target_label": "$TARGET_LABEL",
  "target_id": $TARGET_ID,
  "format": "json",
  "key_mapping": {
    "username": "db_username",
    "password": "db_password"
  }
}
EOF
)

echo "📝 Import Request:"
echo "$payload" | jq '.'
echo ""

# Make the import API call
echo "📤 Sending import request..."
response=$(curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "$payload" \
  "$API_BASE_URL/api/v1/hsm/secrets/import")

echo "📥 Response:"
echo "$response" | jq '.'

# Check if the import was successful
success=$(echo "$response" | jq -r '.success')
if [ "$success" = "true" ]; then
    echo ""
    echo "✅ Secret imported successfully!"
    
    # Extract imported secret info
    label=$(echo "$response" | jq -r '.data.label // "N/A"')
    id=$(echo "$response" | jq -r '.data.id // "N/A"')
    path=$(echo "$response" | jq -r '.data.path // "N/A"')
    
    echo "   Target Label: $label"
    echo "   Target ID: $id"
    echo "   HSM Path: $path"
    
    echo ""
    echo "🔍 Verification commands:"
    echo "   # Check HSM secret via kubectl plugin:"
    echo "   kubectl hsm get $label"
    echo ""
    echo "   # Check HSM secret via API:"
    echo "   curl $API_BASE_URL/api/v1/hsm/secrets/$label"
    echo ""
    echo "   # Check HSMSecret resource:"
    echo "   kubectl get hsmsecret $label"
    echo ""
    echo "   # Check created Kubernetes Secret:"
    echo "   kubectl get secret $label"
    
    echo ""
    echo "📊 Comparison:"
    echo "   Original Secret: kubectl get secret $SOURCE_SECRET -n $SOURCE_NAMESPACE -o yaml"
    echo "   HSM Secret: kubectl get secret $label -o yaml"
    
else
    echo ""
    echo "❌ Failed to import secret!"
    error_code=$(echo "$response" | jq -r '.error.code // "unknown"')
    error_message=$(echo "$response" | jq -r '.error.message // "No error message"')
    echo "   Error Code: $error_code"
    echo "   Error Message: $error_message"
    
    # Show detailed error if available
    if echo "$response" | jq -e '.error.details' >/dev/null; then
        echo "   Error Details:"
        echo "$response" | jq '.error.details'
    fi
    
    exit 1
fi