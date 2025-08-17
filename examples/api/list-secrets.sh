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
    # Extract pagination info
    total=$(echo "$response" | jq -r '.data.total')
    current_page=$(echo "$response" | jq -r '.data.page')
    page_size=$(echo "$response" | jq -r '.data.page_size')
    
    echo "📊 Summary:"
    echo "  Total Secrets: $total"
    echo "  Current Page: $current_page"
    echo "  Page Size: $page_size"
    echo ""
    
    # List secrets
    echo "🔐 Secrets:"
    echo "$response" | jq -r '.data.secrets[] | "  • \(.label) (ID: \(.id)) - Updated: \(.updated_at // "N/A")"'
    
    # Show detailed table if there are secrets
    if [ "$total" -gt 0 ]; then
        echo ""
        echo "📋 Detailed List:"
        echo "$response" | jq -r '
            .data.secrets | 
            ["Label", "ID", "Checksum", "Replicated", "Updated"] as $headers |
            $headers,
            (["-----", "---", "--------", "---------", "-------"]) as $separators |
            $separators,
            (.[] | [.label, (.id // "N/A"), (.checksum[0:8] // "N/A"), .is_replicated, (.updated_at[0:10] // "N/A")]) |
            @tsv
        ' | column -t
    fi
else
    echo "❌ Failed to list secrets!"
    error_code=$(echo "$response" | jq -r '.error.code // "unknown"')
    error_message=$(echo "$response" | jq -r '.error.message // "No error message"')
    echo "   Error Code: $error_code"
    echo "   Error Message: $error_message"
    exit 1
fi