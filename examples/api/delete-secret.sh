#!/bin/bash

# Delete HSM Secret via REST API
# Usage: ./delete-secret.sh [secret-name] [options]

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}
SECRET_NAME="$1"
FORCE=${FORCE:-false}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Check if secret name is provided
if [ -z "$SECRET_NAME" ]; then
    echo "Usage: $0 <secret-name> [--force]"
    echo ""
    echo "Options:"
    echo "  --force    Skip confirmation prompt"
    echo ""
    echo "Environment variables:"
    echo "  API_BASE_URL    API endpoint (default: http://localhost:8090)"
    echo "  FORCE           Skip confirmation (default: false)"
    echo ""
    echo "Examples:"
    echo "  $0 my-secret"
    echo "  $0 my-secret --force"
    echo "  FORCE=true $0 my-secret"
    exit 1
fi

# Parse command line options
while [[ $# -gt 1 ]]; do
    case $2 in
        --force)
            FORCE=true
            shift
            ;;
        *)
            echo "Unknown option: $2"
            exit 1
            ;;
    esac
done

echo -e "${RED}🗑️  Deleting HSM Secret via API...${NC}"
echo "Secret Name: $SECRET_NAME"
echo "API Base URL: $API_BASE_URL"
echo ""

# First, check if the secret exists
echo -e "${BLUE}🔍 Checking if secret exists...${NC}"
check_response=$(curl -s "$API_BASE_URL/api/v1/hsm/secrets/$SECRET_NAME")
check_success=$(echo "$check_response" | jq -r '.success')

if [ "$check_success" != "true" ]; then
    echo -e "${YELLOW}⚠️  Secret '$SECRET_NAME' not found${NC}"
    error_message=$(echo "$check_response" | jq -r '.error.message // "No error message"')
    echo "   Error: $error_message"
    exit 1
fi

echo -e "${GREEN}✅ Secret found${NC}"

# Show secret details
echo ""
echo -e "${BLUE}📋 Secret Details:${NC}"
echo "$check_response" | jq -r '.data | to_entries[] | "  \(.key): \(.value)"'

# Confirmation prompt (unless --force is used)
if [ "$FORCE" != "true" ]; then
    echo ""
    echo -e "${YELLOW}⚠️  This action cannot be undone!${NC}"
    read -p "Are you sure you want to delete secret '$SECRET_NAME'? (y/N): " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "❌ Delete cancelled by user"
        exit 0
    fi
fi

# Make the delete API call
echo ""
echo -e "${BLUE}📤 Sending delete request...${NC}"
response=$(curl -s -X DELETE "$API_BASE_URL/api/v1/hsm/secrets/$SECRET_NAME")

echo -e "${BLUE}📥 Response:${NC}"
echo "$response" | jq '.'

# Check if the request was successful
success=$(echo "$response" | jq -r '.success')
if [ "$success" = "true" ]; then
    echo ""
    echo -e "${GREEN}✅ Secret deleted successfully!${NC}"
    
    # Extract deletion info
    path=$(echo "$response" | jq -r '.data.path // "unknown"')
    message=$(echo "$response" | jq -r '.message // "Secret deleted"')
    
    echo "   Secret Name: $SECRET_NAME"
    echo "   HSM Path: $path"
    echo "   Status: $message"
    
    echo ""
    echo -e "${BLUE}📋 To verify deletion:${NC}"
    echo "   curl $API_BASE_URL/api/v1/hsm/secrets/$SECRET_NAME"
    
    echo ""
    echo -e "${BLUE}📋 To list remaining secrets:${NC}"
    echo "   curl $API_BASE_URL/api/v1/hsm/secrets"
    
else
    echo ""
    echo -e "${RED}❌ Failed to delete secret!${NC}"
    error_code=$(echo "$response" | jq -r '.error.code // "unknown"')
    error_message=$(echo "$response" | jq -r '.error.message // "No error message"')
    echo "   Error Code: $error_code"
    echo "   Error Message: $error_message"
    exit 1
fi