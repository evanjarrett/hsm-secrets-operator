#!/bin/bash

# Build script for HSM Secrets Operator on Talos Linux
# This script builds a custom operator image with PKCS#11 libraries included

set -e

# Configuration
REGISTRY=${REGISTRY:-"localhost:5000"}
IMAGE_NAME=${IMAGE_NAME:-"hsm-secrets-operator"}
TAG=${TAG:-"talos-$(date +%Y%m%d-%H%M)"}
DOCKERFILE=${DOCKERFILE:-"Dockerfile.talos"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check prerequisites
check_prerequisites() {
    print_status "Checking prerequisites..."
    
    if ! command -v docker &> /dev/null; then
        print_error "Docker is required but not installed"
        exit 1
    fi
    
    if ! command -v kubectl &> /dev/null; then
        print_warning "kubectl not found - you won't be able to deploy directly"
    fi
    
    if [ ! -f "$DOCKERFILE" ]; then
        print_error "Dockerfile not found: $DOCKERFILE"
        exit 1
    fi
    
    print_success "Prerequisites check completed"
}

# Build the custom operator image
build_image() {
    print_status "Building HSM Secrets Operator for Talos Linux..."
    print_status "Registry: $REGISTRY"
    print_status "Image: $IMAGE_NAME"
    print_status "Tag: $TAG"
    print_status "Dockerfile: $DOCKERFILE"
    
    # Build multi-arch image if buildx is available
    if docker buildx version &> /dev/null; then
        print_status "Building multi-architecture image with buildx..."
        docker buildx build \
            --platform linux/amd64,linux/arm64 \
            -f "$DOCKERFILE" \
            -t "$REGISTRY/$IMAGE_NAME:$TAG" \
            -t "$REGISTRY/$IMAGE_NAME:talos-latest" \
            --load \
            .
    else
        print_status "Building single-architecture image..."
        docker build \
            -f "$DOCKERFILE" \
            -t "$REGISTRY/$IMAGE_NAME:$TAG" \
            -t "$REGISTRY/$IMAGE_NAME:talos-latest" \
            .
    fi
    
    print_success "Image build completed successfully!"
}

# Test the built image
test_image() {
    print_status "Testing the built image..."
    
    # Test library availability
    print_status "Checking PKCS#11 libraries in image..."
    docker run --rm "$REGISTRY/$IMAGE_NAME:$TAG" /bin/sh -c '
        echo "=== Library Path ==="
        echo "LD_LIBRARY_PATH: $LD_LIBRARY_PATH"
        echo "PKCS11_MODULE_PATH: $PKCS11_MODULE_PATH"
        echo ""
        
        echo "=== Available Libraries ==="
        if [ -d "/usr/local/lib/pkcs11" ]; then
            ls -la /usr/local/lib/pkcs11/
        else
            echo "PKCS#11 directory not found"
        fi
        echo ""
        
        echo "=== Library Dependencies ==="
        for lib in /usr/local/lib/pkcs11/*.so 2>/dev/null; do
            if [ -f "$lib" ]; then
                echo "Testing: $lib"
                if command -v ldd >/dev/null; then
                    ldd "$lib" 2>/dev/null || echo "  Dependencies check not available"
                else
                    echo "  ldd not available in distroless image"
                fi
            fi
        done
    ' || print_warning "Library test had issues (this may be expected with distroless images)"
    
    # Test basic operator startup (dry run)
    print_status "Testing operator startup..."
    timeout 10 docker run --rm "$REGISTRY/$IMAGE_NAME:$TAG" --help || true
    
    print_success "Image testing completed"
}

# Push image to registry
push_image() {
    if [ "$1" = "--push" ] || [ "$1" = "-p" ]; then
        print_status "Pushing images to registry..."
        docker push "$REGISTRY/$IMAGE_NAME:$TAG"
        docker push "$REGISTRY/$IMAGE_NAME:talos-latest"
        print_success "Images pushed successfully!"
    else
        print_status "Skipping push (use --push flag to push to registry)"
        print_status "To push manually:"
        print_status "  docker push $REGISTRY/$IMAGE_NAME:$TAG"
        print_status "  docker push $REGISTRY/$IMAGE_NAME:talos-latest"
    fi
}

# Generate deployment manifests
generate_manifests() {
    print_status "Generating Talos deployment manifests..."
    
    MANIFEST_DIR="deploy/talos"
    mkdir -p "$MANIFEST_DIR"
    
    # Update deployment manifest with new image
    cat > "$MANIFEST_DIR/kustomization.yaml" << EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../default

images:
  - name: controller
    newName: $REGISTRY/$IMAGE_NAME
    newTag: $TAG

patches:
  - patch: |-
      - op: add
        path: /spec/template/spec/nodeSelector
        value:
          kubernetes.io/arch: amd64
          hsm.j5t.io/enabled: "true"
      - op: add
        path: /spec/template/metadata/labels/talos
        value: "enabled"
    target:
      kind: Deployment
      name: hsm-secrets-operator-controller-manager
EOF
    
    print_success "Deployment manifests generated in $MANIFEST_DIR/"
}

# Display usage information
show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Build HSM Secrets Operator image optimized for Talos Linux"
    echo ""
    echo "Options:"
    echo "  --push, -p          Push images to registry after building"
    echo "  --test, -t          Run additional image tests"
    echo "  --manifests, -m     Generate Talos deployment manifests"
    echo "  --help, -h          Show this help message"
    echo ""
    echo "Environment variables:"
    echo "  REGISTRY            Container registry (default: localhost:5000)"
    echo "  IMAGE_NAME          Image name (default: hsm-secrets-operator)"
    echo "  TAG                 Image tag (default: talos-YYYYMMDD-HHMM)"
    echo "  DOCKERFILE          Dockerfile to use (default: Dockerfile.talos)"
    echo ""
    echo "Examples:"
    echo "  $0                  # Build image locally"
    echo "  $0 --push          # Build and push to registry"
    echo "  $0 --test --manifests  # Build, test, and generate manifests"
    echo "  REGISTRY=myregistry.com $0 --push  # Use custom registry"
}

# Main execution
main() {
    print_status "HSM Secrets Operator Talos Build Script"
    print_status "========================================"
    
    # Parse command line arguments
    PUSH=false
    TEST=false
    MANIFESTS=false
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            --push|-p)
                PUSH=true
                shift
                ;;
            --test|-t)
                TEST=true
                shift
                ;;
            --manifests|-m)
                MANIFESTS=true
                shift
                ;;
            --help|-h)
                show_usage
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done
    
    # Execute build steps
    check_prerequisites
    build_image
    
    if [ "$TEST" = true ]; then
        test_image
    fi
    
    if [ "$PUSH" = true ]; then
        push_image --push
    else
        push_image
    fi
    
    if [ "$MANIFESTS" = true ]; then
        generate_manifests
    fi
    
    print_success "Build process completed!"
    print_status "Image: $REGISTRY/$IMAGE_NAME:$TAG"
    
    if command -v kubectl &> /dev/null && [ "$MANIFESTS" = true ]; then
        print_status "To deploy on Talos:"
        print_status "  kubectl apply -k deploy/talos/"
    elif command -v kubectl &> /dev/null; then
        print_status "To deploy on Talos:"
        print_status "  kubectl apply -f examples/advanced/talos-deployment.yaml"
    fi
}

# Run main function with all arguments
main "$@"