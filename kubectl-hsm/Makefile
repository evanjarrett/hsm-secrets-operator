# kubectl-hsm Plugin Makefile
# Copyright 2025. Licensed under the Apache License, Version 2.0.

# Build variables
PLUGIN_NAME=kubectl-hsm
VERSION?=dev
BUILD_DIR=bin
DIST_DIR=dist

# Go build settings
GO_VERSION=1.24
CGO_ENABLED=0
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

# Supported platforms for cross-compilation
PLATFORMS = \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

# Default target
.PHONY: all
all: build

# Build for current platform
.PHONY: build
build:
	@echo "Building kubectl-hsm for current platform..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(PLUGIN_NAME) ./cmd

# Build for all supported platforms
.PHONY: build-all
build-all: clean-dist
	@echo "Building kubectl-hsm for all platforms..."
	@mkdir -p $(DIST_DIR)
	@$(foreach platform,$(PLATFORMS), \
		echo "Building for $(platform)..."; \
		GOOS=$(shell echo $(platform) | cut -d'/' -f1) \
		GOARCH=$(shell echo $(platform) | cut -d'/' -f2) \
		CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) \
			-o $(DIST_DIR)/$(PLUGIN_NAME)-$(shell echo $(platform) | sed 's/\//-/')$(shell [ "$(shell echo $(platform) | cut -d'/' -f1)" = "windows" ] && echo ".exe" || echo "") ./cmd \
		&& echo "✅ Built $(DIST_DIR)/$(PLUGIN_NAME)-$(shell echo $(platform) | sed 's/\//-/')$(shell [ "$(shell echo $(platform) | cut -d'/' -f1)" = "windows" ] && echo ".exe" || echo "")";)

# Install to local bin directory (for testing)
.PHONY: install
install: build
	@echo "Installing kubectl-hsm to ~/bin/..."
	@mkdir -p ~/bin
	@cp $(BUILD_DIR)/$(PLUGIN_NAME) ~/bin/
	@echo "✅ Installed to ~/bin/$(PLUGIN_NAME)"
	@echo ""
	@echo "To use the plugin:"
	@echo "  1. Ensure ~/bin is in your PATH"
	@echo "  2. Run: kubectl hsm --help"

# Install to system-wide location (requires sudo)
.PHONY: install-system
install-system: build
	@echo "Installing kubectl-hsm to /usr/local/bin/..."
	@sudo cp $(BUILD_DIR)/$(PLUGIN_NAME) /usr/local/bin/
	@echo "✅ Installed to /usr/local/bin/$(PLUGIN_NAME)"
	@echo ""
	@echo "Plugin is now available system-wide:"
	@echo "  kubectl hsm --help"

# Create installation script
.PHONY: install-script
install-script:
	@echo "Creating installation script..."
	@mkdir -p $(DIST_DIR)
	@echo '#!/bin/bash' > $(DIST_DIR)/install.sh
	@echo 'set -e' >> $(DIST_DIR)/install.sh
	@echo '' >> $(DIST_DIR)/install.sh
	@echo '# kubectl-hsm Installation Script' >> $(DIST_DIR)/install.sh
	@echo '# This script downloads and installs the kubectl-hsm plugin' >> $(DIST_DIR)/install.sh
	@echo '' >> $(DIST_DIR)/install.sh
	@echo 'PLUGIN_NAME="kubectl-hsm"' >> $(DIST_DIR)/install.sh
	@echo 'VERSION="$${VERSION:-latest}"' >> $(DIST_DIR)/install.sh
	@echo 'INSTALL_DIR="$${INSTALL_DIR:-/usr/local/bin}"' >> $(DIST_DIR)/install.sh
	@echo '' >> $(DIST_DIR)/install.sh
	@echo 'echo "Installing kubectl-hsm..."' >> $(DIST_DIR)/install.sh
	@echo 'echo "✅ kubectl-hsm installation script created"' >> $(DIST_DIR)/install.sh
	@chmod +x $(DIST_DIR)/install.sh
	@echo "✅ Created installation script: $(DIST_DIR)/install.sh"

# Create archive packages for distribution
.PHONY: package
package: build-all install-script
	@echo "Creating distribution packages..."
	@$(foreach platform,$(PLATFORMS), \
		os=$$(echo $(platform) | cut -d'/' -f1); \
		arch=$$(echo $(platform) | cut -d'/' -f2); \
		binary="$(PLUGIN_NAME)-$$os-$$arch"; \
		[ "$$os" = "windows" ] && binary="$$binary.exe"; \
		archive_name="$(PLUGIN_NAME)-$(VERSION)-$$os-$$arch"; \
		if [ "$$os" = "windows" ]; then \
			(cd $(DIST_DIR) && zip "$$archive_name.zip" "$$binary" install.sh README.md 2>/dev/null || zip "$$archive_name.zip" "$$binary" install.sh); \
		else \
			(cd $(DIST_DIR) && tar czf "$$archive_name.tar.gz" "$$binary" install.sh README.md 2>/dev/null || tar czf "$$archive_name.tar.gz" "$$binary" install.sh); \
		fi; \
		echo "📦 Created $$archive_name archive";)

# Generate checksums for release artifacts
.PHONY: checksums
checksums: package
	@echo "Generating checksums..."
	@cd $(DIST_DIR) && find . -name "*.tar.gz" -o -name "*.zip" | xargs shasum -a 256 > SHA256SUMS
	@echo "✅ Generated checksums: $(DIST_DIR)/SHA256SUMS"

# Test the built binary
.PHONY: test-binary
test-binary: build
	@echo "Testing built binary..."
	@$(BUILD_DIR)/$(PLUGIN_NAME) version
	@echo "✅ Binary test passed"

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build directory..."
	@rm -rf $(BUILD_DIR)

.PHONY: clean-dist
clean-dist:
	@echo "Cleaning distribution directory..."
	@rm -rf $(DIST_DIR)

.PHONY: clean-all
clean-all: clean clean-dist

# Development targets
.PHONY: dev
dev: build test-binary
	@echo "Development build complete"

.PHONY: dev-install
dev-install: dev install
	@echo "Development installation complete"

# Help target
.PHONY: help
help:
	@echo "kubectl-hsm Plugin Build System"
	@echo ""
	@echo "Targets:"
	@echo "  build         - Build for current platform"
	@echo "  build-all     - Build for all supported platforms"
	@echo "  install       - Install to ~/bin/"
	@echo "  install-system - Install to /usr/local/bin/ (requires sudo)"
	@echo "  install-script - Generate installation script"
	@echo "  package       - Create distribution packages"
	@echo "  checksums     - Generate checksums for packages"
	@echo "  test-binary   - Test the built binary"
	@echo "  dev           - Development build and test"
	@echo "  dev-install   - Development build, test, and install"
	@echo "  clean         - Clean build artifacts"
	@echo "  clean-all     - Clean all artifacts"
	@echo "  help          - Show this help"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION       - Plugin version (default: dev)"
	@echo "  INSTALL_DIR   - Installation directory (default: /usr/local/bin)"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make build-all VERSION=v1.0.0"
	@echo "  make install"
	@echo "  make package VERSION=v1.0.0"

# Show supported platforms
.PHONY: platforms
platforms:
	@echo "Supported platforms:"
	@$(foreach platform,$(PLATFORMS),echo "  $(platform)";)