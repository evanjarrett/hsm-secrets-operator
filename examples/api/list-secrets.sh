#!/bin/bash

# List all HSM secrets via REST API
# Usage: ./list-secrets.sh [page] [page_size]

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}
PAGE=${1:-1}
PAGE_SIZE=${2:-10}

echo "📋 Listing HSM Secrets via API..."
echo "API Base URL: $API_BASE_URL"
echo "Page: $PAGE, Page Size: $PAGE_SIZE"
echo ""

# Make the API call
response=$(curl -s "$API_BASE_URL/api/v1/hsm/secrets?page=$PAGE&page_size=$PAGE_SIZE")

# Check if the request was successful
success=$(echo "$response" | jq -r '.success')
if [ "$success" = "true" ]; then
    # Extract secret info
    count=$(echo "$response" | jq -r '.data.count')
    prefix=$(echo "$response" | jq -r '.data.prefix // ""')
    
    echo "📊 Summary:"
    echo "  Total Secrets: $count"
    if [ -n "$prefix" ] && [ "$prefix" != "" ]; then
        echo "  Prefix Filter: $prefix"
    fi
    echo ""
    
    # List secrets
    echo "🔐 Secrets:"
    if [ "$count" -gt 0 ]; then
        echo "$response" | jq -r '.data.paths[] | "  • \(.)"'
    else
        echo "  No secrets found"
    fi
else
    echo "❌ Failed to list secrets!"
    error_code=$(echo "$response" | jq -r '.error.code // "unknown"')
    error_message=$(echo "$response" | jq -r '.error.message // "No error message"')
    echo "   Error Code: $error_code"
    echo "   Error Message: $error_message"
    exit 1
fi