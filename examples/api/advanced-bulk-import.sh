#!/bin/bash

# Advanced bulk import script with validation and rollback
# Automatically uses kubectl-hsm plugin if available, falls back to REST API
# Usage: ./advanced-bulk-import.sh [config-file] [options]

set -e

API_BASE_URL=${API_BASE_URL:-"http://localhost:8090"}
CONFIG_FILE=${1:-"production-import.json"}
DRY_RUN=${DRY_RUN:-false}
ROLLBACK_ON_FAILURE=${ROLLBACK_ON_FAILURE:-true}
MAX_PARALLEL=${MAX_PARALLEL:-5}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✅${NC} $1"
}

error() {
    echo -e "${RED}❌${NC} $1"
}

warning() {
    echo -e "${YELLOW}⚠️${NC} $1"
}

# Convert Bitwarden vault format to HSM format
convert_bitwarden_vault() {
    local input_file="$1"
    local output_file="${input_file%.json}-hsm.json"
    
    log "Converting Bitwarden vault format to HSM format..."
    
    # Check if this is a Bitwarden vault file
    if jq -e '.projects and .secrets' "$input_file" > /dev/null 2>&1; then
        log "Detected Bitwarden vault format"
        
        # Build project name mapping
        local project_map=$(mktemp)
        jq -r '.projects[] | "\(.id) \(.name)"' "$input_file" > "$project_map"
        
        # Convert secrets format
        jq --slurpfile projects <(jq '.projects' "$input_file") '
        {
          "secrets": [
            .secrets[] | {
              "label": .key,
              "id": (.id | gsub("-"; "") | .[0:8]),
              "format": "text",
              "description": (if .note != "" then .note else "Imported from Bitwarden vault" end),
              "tags": {
                "source": "bitwarden",
                "projects": [.projectIds[] as $pid | $projects[0][] | select(.id == $pid) | .name]
              },
              "metadata": {
                "label": .key,
                "description": (if .note != "" then .note else "Imported from Bitwarden vault" end),
                "tags": {
                  "source": "bitwarden",
                  "projects": [.projectIds[] as $pid | $projects[0][] | select(.id == $pid) | .name]
                },
                "format": "text",
                "dataType": "plaintext",
                "createdAt": now | strftime("%Y-%m-%dT%H:%M:%SZ"),
                "source": "bitwarden"
              },
              "data": {
                "value": .value
              }
            }
          ]
        }' "$input_file" > "$output_file"
        
        rm "$project_map"
        success "Converted Bitwarden vault to: $output_file"
        CONFIG_FILE="$output_file"
    else
        log "Not a Bitwarden vault format, proceeding with original file"
    fi
}

# Validate prerequisites
validate_prerequisites() {
    log "Validating prerequisites..."
    
    if ! command -v jq &> /dev/null; then
        error "jq is required but not installed"
        exit 1
    fi
    
    if ! command -v curl &> /dev/null; then
        error "curl is required but not installed"
        exit 1
    fi
    
    if [ ! -f "$CONFIG_FILE" ]; then
        error "Config file not found: $CONFIG_FILE"
        exit 1
    fi
    
    if ! jq empty "$CONFIG_FILE" 2>/dev/null; then
        error "Invalid JSON in config file: $CONFIG_FILE"
        exit 1
    fi
    
    # Convert Bitwarden format if detected
    convert_bitwarden_vault "$CONFIG_FILE"
    
    # Test API connectivity
    if ! curl -s --connect-timeout 5 "$API_BASE_URL/api/v1/health" > /dev/null; then
        error "Cannot connect to API at: $API_BASE_URL"
        exit 1
    fi
    
    success "Prerequisites validated"
}

# Pre-import validation
validate_config() {
    log "Validating configuration..."
    
    local issues=0
    
    # Check for duplicate labels
    duplicate_labels=$(jq -r '.secrets[].label' "$CONFIG_FILE" | sort | uniq -d)
    if [ -n "$duplicate_labels" ]; then
        error "Duplicate labels found:"
        echo "$duplicate_labels" | while IFS= read -r label; do
            echo "  - $label"
        done
        ((issues++))
    fi
    
    # Check for duplicate IDs
    duplicate_ids=$(jq -r '.secrets[].id' "$CONFIG_FILE" | sort | uniq -d)
    if [ -n "$duplicate_ids" ]; then
        error "Duplicate IDs found:"
        echo "$duplicate_ids" | while IFS= read -r id; do
            echo "  - $id"
        done
        ((issues++))
    fi
    
    # Validate required fields
    jq -c '.secrets[]' "$CONFIG_FILE" | while IFS= read -r secret; do
        label=$(echo "$secret" | jq -r '.label')
        id=$(echo "$secret" | jq -r '.id')
        
        if [ "$label" = "null" ] || [ -z "$label" ]; then
            error "Secret missing label"
            ((issues++))
        fi
        
        if [ "$id" = "null" ] || [ -z "$id" ]; then
            error "Secret '$label' missing ID"
            ((issues++))
        fi
        
        if ! echo "$secret" | jq -e '.data' > /dev/null; then
            error "Secret '$label' missing data"
            ((issues++))
        fi
    done
    
    if [ $issues -gt 0 ]; then
        error "Configuration validation failed with $issues issues"
        exit 1
    fi
    
    success "Configuration validated"
}

# Check for existing secrets
check_existing_secrets() {
    log "Checking for existing secrets..."
    
    local conflicts=()
    
    while IFS= read -r label; do
        # Try kubectl-hsm first if available
        if command -v kubectl >/dev/null && kubectl hsm --help >/dev/null 2>&1; then
            if kubectl hsm get "$label" >/dev/null 2>&1; then
                conflicts+=("$label")
                continue
            fi
        fi
        
        # Fallback to API
        response=$(curl -s "$API_BASE_URL/api/v1/hsm/secrets/$label")
        success=$(echo "$response" | jq -r '.success')
        
        if [ "$success" = "true" ]; then
            conflicts+=("$label")
        fi
    done <<< "$(jq -r '.secrets[].label' "$CONFIG_FILE")"
    
    if [ ${#conflicts[@]} -gt 0 ]; then
        warning "Found ${#conflicts[@]} existing secrets that will be overwritten:"
        for conflict in "${conflicts[@]}"; do
            echo "  - $conflict"
        done
        
        if [ "$DRY_RUN" = "false" ]; then
            read -p "Continue? (y/N): " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                log "Import cancelled by user"
                exit 0
            fi
        fi
    else
        success "No conflicts found"
    fi
}

# Import a single secret
import_secret() {
    local secret_data="$1"
    local label=$(echo "$secret_data" | jq -r '.label')
    
    if [ "$DRY_RUN" = "true" ]; then
        echo "[DRY RUN] Would import: $label"
        return 0
    fi
    
    log "Importing: $label"
    
    response=$(curl -s -X POST \
      -H "Content-Type: application/json" \
      -d "$secret_data" \
      "$API_BASE_URL/api/v1/hsm/secrets/$label" 2>/dev/null)
    
    if [ $? -ne 0 ]; then
        error "Failed to connect to API for $label"
        return 1
    fi
    
    success_status=$(echo "$response" | jq -r '.success')
    if [ "$success_status" = "true" ]; then
        success "Imported: $label"
        return 0
    else
        error_message=$(echo "$response" | jq -r '.error.message // "Unknown error"')
        error "Failed to import $label: $error_message"
        return 1
    fi
}

# Rollback imported secrets
rollback_secrets() {
    local imported_secrets=("$@")
    
    if [ ${#imported_secrets[@]} -eq 0 ]; then
        return 0
    fi
    
    warning "Rolling back ${#imported_secrets[@]} imported secrets..."
    
    for label in "${imported_secrets[@]}"; do
        log "Rolling back: $label"
        
        # Try kubectl-hsm first if available
        if command -v kubectl >/dev/null && kubectl hsm --help >/dev/null 2>&1; then
            kubectl hsm delete "$label" --force >/dev/null 2>&1 || \
            curl -s -X DELETE "$API_BASE_URL/api/v1/hsm/secrets/$label" > /dev/null
        else
            curl -s -X DELETE "$API_BASE_URL/api/v1/hsm/secrets/$label" > /dev/null
        fi
    done
    
    warning "Rollback completed"
}

# Main import process
perform_import() {
    log "Starting bulk import..."
    
    local total_secrets=$(jq '.secrets | length' "$CONFIG_FILE")
    local imported_secrets=()
    local failed_secrets=()
    local success_count=0
    local failure_count=0
    
    log "Importing $total_secrets secrets..."
    
    # Process secrets sequentially for better error handling
    while IFS= read -r secret_json; do
        label=$(echo "$secret_json" | jq -r '.label')
        
        if import_secret "$secret_json"; then
            imported_secrets+=("$label")
            ((success_count++))
        else
            failed_secrets+=("$label")
            ((failure_count++))
            
            # Rollback on first failure if enabled
            if [ "$ROLLBACK_ON_FAILURE" = "true" ] && [ $failure_count -eq 1 ]; then
                error "First failure detected, initiating rollback..."
                rollback_secrets "${imported_secrets[@]}"
                exit 1
            fi
        fi
        
        # Progress indicator
        local current=$((success_count + failure_count))
        log "Progress: $current/$total_secrets"
        
    done < <(jq -c '.secrets[]' "$CONFIG_FILE")
    
    # Summary
    echo ""
    log "Import Summary:"
    success "Successfully imported: $success_count secrets"
    if [ $failure_count -gt 0 ]; then
        error "Failed to import: $failure_count secrets"
        if [ ${#failed_secrets[@]} -gt 0 ]; then
            echo "Failed secrets:"
            for failed in "${failed_secrets[@]}"; do
                echo "  - $failed"
            done
        fi
    fi
    
    # Generate import report
    local report_file="import-report-$(date +%Y%m%d-%H%M%S).json"
    cat > "$report_file" <<EOF
{
  "timestamp": "$(date -Iseconds)",
  "config_file": "$CONFIG_FILE",
  "api_url": "$API_BASE_URL",
  "total_secrets": $total_secrets,
  "successful_imports": $success_count,
  "failed_imports": $failure_count,
  "imported_secrets": $(printf '%s\n' "${imported_secrets[@]}" | jq -R . | jq -s .),
  "failed_secrets": $(printf '%s\n' "${failed_secrets[@]}" | jq -R . | jq -s .)
}
EOF
    
    log "Import report saved to: $report_file"
    
    if [ $failure_count -eq 0 ]; then
        success "All secrets imported successfully!"
        exit 0
    else
        error "Some secrets failed to import"
        exit 1
    fi
}

# Parse command line options
while [[ $# -gt 0 ]]; do
    case $1 in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --no-rollback)
            ROLLBACK_ON_FAILURE=false
            shift
            ;;
        --api-url)
            API_BASE_URL="$2"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [config-file] [options]"
            echo ""
            echo "Supports both HSM format and Bitwarden vault format (auto-detected)."
            echo ""
            echo "Options:"
            echo "  --dry-run        Show what would be imported without making changes"
            echo "  --no-rollback    Don't rollback on failure"
            echo "  --api-url URL    Override API base URL"
            echo "  --help           Show this help message"
            echo ""
            echo "Config file formats:"
            echo "  HSM format:      Standard format with 'secrets' array"
            echo "  Bitwarden:       Vault export with 'projects' and 'secrets' arrays"
            echo ""
            echo "Environment variables:"
            echo "  API_BASE_URL           API endpoint (default: http://localhost:8090)"
            echo "  DRY_RUN                Enable dry run mode (default: false)"
            echo "  ROLLBACK_ON_FAILURE    Enable rollback on failure (default: true)"
            exit 0
            ;;
        *)
            CONFIG_FILE="$1"
            shift
            ;;
    esac
done

# Main execution
echo "🔐 Advanced HSM Secrets Bulk Import"
echo "====================================="
echo "Config file: $CONFIG_FILE"
echo "API URL: $API_BASE_URL"
echo "Dry run: $DRY_RUN"
echo "Rollback on failure: $ROLLBACK_ON_FAILURE"
echo ""

validate_prerequisites
validate_config
check_existing_secrets
perform_import