#!/bin/bash

# HSM Secrets Operator API Health Check
# This script checks the health of the API server and HSM devices

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}

echo "🔍 Checking HSM Secrets Operator API Health..."
echo "API Base URL: $API_BASE_URL"
echo ""

# Function to make API calls with error handling
api_call() {
    local method="$1"
    local endpoint="$2"
    local data="$3"
    
    if [ -n "$data" ]; then
        curl -s -X "$method" \
             -H "Content-Type: application/json" \
             -d "$data" \
             "$API_BASE_URL$endpoint"
    else
        curl -s -X "$method" "$API_BASE_URL$endpoint"
    fi
}

# Check API health endpoint
echo "📊 API Health Status:"
health_response=$(api_call GET "/api/v1/health")
echo "$health_response" | jq '.'

# Extract health information
status=$(echo "$health_response" | jq -r '.data.status')
hsm_connected=$(echo "$health_response" | jq -r '.data.hsm_connected')
replication_enabled=$(echo "$health_response" | jq -r '.data.replication_enabled')
active_nodes=$(echo "$health_response" | jq -r '.data.active_nodes')

echo ""
echo "🏥 Health Summary:"
echo "  Overall Status: $status"
echo "  HSM Connected: $hsm_connected"
echo "  Replication Enabled: $replication_enabled"
echo "  Active Nodes: $active_nodes"

# Check if API is healthy
if [ "$status" = "healthy" ]; then
    echo "  ✅ API is healthy"
    exit_code=0
else
    echo "  ❌ API is not healthy"
    exit_code=1
fi

# Check HSM connectivity
if [ "$hsm_connected" = "true" ]; then
    echo "  ✅ HSM is connected"
else
    echo "  ❌ HSM is not connected"
    exit_code=1
fi

echo ""
echo "📋 Additional Checks:"

# Test basic API functionality
echo "  Testing secret listing endpoint..."
secrets_response=$(api_call GET "/api/v1/hsm/secrets" 2>/dev/null)
if [ $? -eq 0 ]; then
    secret_count=$(echo "$secrets_response" | jq -r '.data.total // 0')
    echo "  ✅ Secrets endpoint working (found $secret_count secrets)"
else
    echo "  ❌ Secrets endpoint failed"
    exit_code=1
fi

# Test API response format
echo "  Validating API response format..."
success=$(echo "$health_response" | jq -r '.success')
if [ "$success" = "true" ]; then
    echo "  ✅ API response format is valid"
else
    echo "  ❌ API response format is invalid"
    exit_code=1
fi

echo ""
if [ $exit_code -eq 0 ]; then
    echo "🎉 All health checks passed!"
else
    echo "❌ Some health checks failed!"
fi

exit $exit_code