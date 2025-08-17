#!/bin/bash
# HSM Secrets Operator Helm Chart Installation Script

set -euo pipefail

# Configuration
CHART_REPO_URL="https://evanjarrett.github.io/hsm-secrets-operator/"
CHART_NAME="hsm-secrets-operator"
RELEASE_NAME="hsm-secrets-operator"
NAMESPACE="hsm-secrets-operator-system"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

usage() {
    cat << EOF
HSM Secrets Operator Installation Script

Usage: $0 [OPTIONS]

Options:
  -h, --help          Show this help message
  -n, --namespace     Kubernetes namespace (default: ${NAMESPACE})
  -r, --release       Helm release name (default: ${RELEASE_NAME})
  --mock              Install with mock HSM client (default)
  --pkcs11            Install with PKCS#11 HSM client
  --library           PKCS#11 library path (default: /usr/lib/x86_64-linux-gnu/opensc-pkcs11.so)
  --slot-id           PKCS#11 slot ID (default: 0)
  --pin-secret        Name of secret containing HSM PIN (required for PKCS#11)
  --examples          Install with example resources
  --monitoring        Enable Prometheus ServiceMonitor
  --dry-run           Show what would be installed without actually installing
  --upgrade           Upgrade existing installation
  --uninstall         Uninstall the operator

Examples:
  # Install with mock HSM (for testing)
  $0 --mock --examples

  # Install with PKCS#11 HSM
  kubectl create secret generic hsm-pin --from-literal=pin=your-hsm-pin
  $0 --pkcs11 --pin-secret hsm-pin

  # Upgrade existing installation
  $0 --upgrade --monitoring

  # Uninstall
  $0 --uninstall
EOF
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    
    if ! command -v helm &> /dev/null; then
        log_error "helm is not installed or not in PATH"
        exit 1
    fi
    
    # Check if cluster is accessible
    if ! kubectl cluster-info &> /dev/null; then
        log_error "Cannot connect to Kubernetes cluster"
        exit 1
    fi
    
    log_success "Prerequisites checked"
}

add_helm_repo() {
    log_info "Adding Helm repository..."
    if helm repo list | grep -q "${CHART_NAME}"; then
        log_info "Repository already exists, updating..."
    else
        helm repo add "${CHART_NAME}" "${CHART_REPO_URL}"
    fi
    helm repo update "${CHART_NAME}"
    log_success "Helm repository ready"
}

create_namespace() {
    log_info "Creating namespace ${NAMESPACE}..."
    kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
    log_success "Namespace ${NAMESPACE} ready"
}

install_operator() {
    local helm_args=()
    
    # Build helm arguments
    if [[ "${DRY_RUN}" == "true" ]]; then
        helm_args+=(--dry-run --debug)
        log_info "Performing dry run..."
    fi
    
    if [[ "${UPGRADE}" == "true" ]]; then
        local cmd="upgrade"
        helm_args+=(--install)
        log_info "Upgrading operator..."
    else
        local cmd="install"
        log_info "Installing operator..."
    fi
    
    helm_args+=(--namespace "${NAMESPACE}")
    helm_args+=(--create-namespace)
    
    # HSM configuration
    if [[ "${HSM_TYPE}" == "pkcs11" ]]; then
        helm_args+=(--set hsm.clientType=pkcs11)
        helm_args+=(--set hsm.pkcs11.library="${PKCS11_LIBRARY}")
        helm_args+=(--set hsm.pkcs11.slotId="${PKCS11_SLOT_ID}")
        if [[ -n "${PIN_SECRET}" ]]; then
            helm_args+=(--set hsm.pkcs11.pinSecret.name="${PIN_SECRET}")
        fi
    else
        helm_args+=(--set hsm.clientType=mock)
    fi
    
    # Optional features
    if [[ "${EXAMPLES}" == "true" ]]; then
        helm_args+=(--set examples.hsmsecret.enabled=true)
        helm_args+=(--set examples.hsmdevice.enabled=true)
    fi
    
    if [[ "${MONITORING}" == "true" ]]; then
        helm_args+=(--set metrics.serviceMonitor.enabled=true)
    fi
    
    # Execute helm command
    helm "${cmd}" "${RELEASE_NAME}" "${CHART_NAME}/${CHART_NAME}" "${helm_args[@]}"
    
    if [[ "${DRY_RUN}" != "true" ]]; then
        log_success "Operator installed successfully!"
        
        # Wait for deployment
        log_info "Waiting for deployment to be ready..."
        kubectl wait --for=condition=available deployment/${RELEASE_NAME}-controller-manager \
            --namespace="${NAMESPACE}" --timeout=300s
        
        log_success "Deployment is ready!"
        
        # Show status
        show_status
    fi
}

uninstall_operator() {
    log_info "Uninstalling operator..."
    
    if helm list --namespace "${NAMESPACE}" | grep -q "${RELEASE_NAME}"; then
        helm uninstall "${RELEASE_NAME}" --namespace "${NAMESPACE}"
        log_success "Operator uninstalled"
    else
        log_warn "Operator not found"
    fi
    
    # Optionally remove namespace
    read -p "Remove namespace ${NAMESPACE}? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        kubectl delete namespace "${NAMESPACE}" --ignore-not-found
        log_success "Namespace removed"
    fi
}

show_status() {
    log_info "Current status:"
    echo
    
    # Helm release status
    helm status "${RELEASE_NAME}" --namespace "${NAMESPACE}" || true
    echo
    
    # Pod status
    log_info "Pods in namespace ${NAMESPACE}:"
    kubectl get pods --namespace="${NAMESPACE}" || true
    echo
    
    # Custom resources
    log_info "Custom resources:"
    kubectl get hsmsecrets,hsmdevices --all-namespaces 2>/dev/null || log_warn "No custom resources found"
    echo
    
    # Connection information
    log_info "To access the API server:"
    echo "  kubectl port-forward svc/${RELEASE_NAME}-api 8080:8080 --namespace=${NAMESPACE}"
    echo
    
    log_info "To view logs:"
    echo "  kubectl logs -f deployment/${RELEASE_NAME}-controller-manager --namespace=${NAMESPACE}"
}

# Default values
NAMESPACE="hsm-secrets-operator-system"
RELEASE_NAME="hsm-secrets-operator"
HSM_TYPE="mock"
PKCS11_LIBRARY="/usr/lib/x86_64-linux-gnu/opensc-pkcs11.so"
PKCS11_SLOT_ID="0"
PIN_SECRET=""
EXAMPLES="false"
MONITORING="false"
DRY_RUN="false"
UPGRADE="false"
UNINSTALL="false"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -r|--release)
            RELEASE_NAME="$2"
            shift 2
            ;;
        --mock)
            HSM_TYPE="mock"
            shift
            ;;
        --pkcs11)
            HSM_TYPE="pkcs11"
            shift
            ;;
        --library)
            PKCS11_LIBRARY="$2"
            shift 2
            ;;
        --slot-id)
            PKCS11_SLOT_ID="$2"
            shift 2
            ;;
        --pin-secret)
            PIN_SECRET="$2"
            shift 2
            ;;
        --examples)
            EXAMPLES="true"
            shift
            ;;
        --monitoring)
            MONITORING="true"
            shift
            ;;
        --dry-run)
            DRY_RUN="true"
            shift
            ;;
        --upgrade)
            UPGRADE="true"
            shift
            ;;
        --uninstall)
            UNINSTALL="true"
            shift
            ;;
        *)
            log_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Validation
if [[ "${HSM_TYPE}" == "pkcs11" && -z "${PIN_SECRET}" ]]; then
    log_error "PKCS#11 configuration requires --pin-secret to be specified"
    log_info "Create the secret first: kubectl create secret generic hsm-pin --from-literal=pin=your-hsm-pin"
    exit 1
fi

# Main execution
if [[ "${UNINSTALL}" == "true" ]]; then
    uninstall_operator
else
    check_prerequisites
    add_helm_repo
    create_namespace
    install_operator
fi