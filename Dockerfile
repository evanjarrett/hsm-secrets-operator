# Multi-stage distroless Dockerfile for maximum security with USB access
# Phase 2: Root + Distroless - compensates for root requirement with minimal attack surface

# Stage 1: Go builder (also serves as dependency source)
FROM golang:1.24-trixie AS builder
ARG TARGETOS
ARG TARGETARCH

# Install both runtime and development packages in single stage
RUN apt-get update && apt-get install -y \
    opensc \
    pcscd \
    libccid \
    libpcsclite1 \
    libusb-1.0-0 \
    udev \
    ca-certificates \
    libudev-dev \
    libpcsclite-dev \
    libusb-1.0-0-dev \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Create necessary runtime directories
RUN mkdir -p /run/pcscd /var/run/pcscd /var/lock/pcsc && \
    chmod 755 /run/pcscd /var/run/pcscd /var/lock/pcsc

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
COPY web/ web/

# Build with CGO enabled for PKCS#11 support
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o hsm-operator cmd/hsm-operator/main.go

# Collect all runtime dependencies recursively using ldd
RUN echo "Discovering runtime dependencies (recursive)..." && \
    # Define binaries and libraries to scan for dependencies
    SCAN_BINARIES="/workspace/hsm-operator /usr/sbin/pcscd /usr/bin/pkcs11-tool" && \
    SCAN_LIBRARIES="/usr/lib/*/opensc-pkcs11.so /usr/lib/*/libpcsclite.so.1" && \
    SCAN_TARGETS="$SCAN_BINARIES $SCAN_LIBRARIES" && \
    mkdir -p /runtime-deps && \
    touch /tmp/deps_initial.txt && \
    # Get initial dependencies from main binaries using ldd
    echo "Running ldd on: $SCAN_TARGETS" && \
    for target in $SCAN_TARGETS; do \
        ldd $target 2>/dev/null | grep "=>" | awk '{print $3}' | grep -v "^$" || true; \
    done >> /tmp/deps_initial.txt && \
    ldd /workspace/hsm-operator 2>/dev/null | grep -o '/lib.*/ld-linux[^ ]*' >> /tmp/deps_initial.txt || true && \
    # Now recursively check all those libraries for their dependencies
    for lib in $(cat /tmp/deps_initial.txt); do \
        if [ -f "$lib" ]; then \
            ldd "$lib" 2>/dev/null | grep "=>" | awk '{print $3}' | grep -v "^$" || true; \
        fi; \
    done >> /tmp/deps_initial.txt && \
    # Find dlopen'd libraries by scanning for .so references in binaries
    echo "Scanning for dlopen'd libraries in: $SCAN_TARGETS" && \
    for target in $SCAN_TARGETS; do \
        if [ -f "$target" ]; then \
            strings "$target" 2>/dev/null | grep -E '\.so(\.[0-9]+)*$' | while read soname; do \
                find /usr/lib /lib -name "$soname" 2>/dev/null || true; \
            done; \
        fi; \
    done >> /tmp/deps_initial.txt && \
    # Deduplicate and copy
    sort -u /tmp/deps_initial.txt > /tmp/deps.txt && \
    echo "Found $(wc -l < /tmp/deps.txt) unique library dependencies:" && \
    cat /tmp/deps.txt && \
    for lib in $(cat /tmp/deps.txt); do \
        if [ -f "$lib" ]; then \
            dir=$(dirname "$lib"); \
            mkdir -p "/runtime-deps$dir"; \
            cp -L "$lib" "/runtime-deps$lib"; \
        fi; \
    done && \
    echo "Dependencies collected to /runtime-deps" && \
    # Verify all binaries can find their dependencies
    echo "Testing binaries for missing dependencies..." && \
    for binary in $SCAN_BINARIES; do \
        echo "Testing $binary..."; \
        ldd "$binary" 2>&1 | grep "not found" && echo "ERROR: Missing dependencies for $binary" && exit 1 || true; \
    done && \
    echo "All binaries have satisfied dependencies"

# Stage 2: Base runtime stage with all files
FROM gcr.io/distroless/static-debian12:debug AS runtime

# Copy essential system files
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Copy all runtime library dependencies (auto-discovered via ldd)
COPY --from=builder /runtime-deps /

# Copy PKCS#11 library (loaded via dlopen by Go app at runtime with user-specified path)
COPY --from=builder /usr/lib/*/opensc-pkcs11.so /usr/lib/pkcs11/
# Note: Most other dlopen'd libraries (libpcsclite_real.so.1, libopensc.so.12, etc) are
# auto-discovered via the enhanced dependency scanning that detects .so references in binaries

# Copy essential binaries
COPY --from=builder /usr/sbin/pcscd /usr/sbin/
COPY --from=builder /usr/bin/pkcs11-tool /usr/bin/

# Copy udev rules for HSM devices (CCID support)
COPY --from=builder /lib/udev/rules.d/92-libccid.rules /lib/udev/rules.d/

# Copy CCID drivers for pcscd (Debian Trixie provides v1.6.2 with native Pico HSM multi-interface support)
COPY --from=builder /usr/lib/pcsc /usr/lib/pcsc

# Copy CCID configuration file (needed for Info.plist symlink)
COPY --from=builder /etc/libccid_Info.plist /etc/

# Copy CA certificates
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy runtime directories
COPY --from=builder /var/run/pcscd /run/pcscd
COPY --from=builder /var/lock/pcsc /var/lock/pcsc

# Copy application binary and entrypoint
COPY --from=builder /workspace/hsm-operator /hsm-operator
COPY --chmod=755 entrypoint.sh /entrypoint.sh

# Runtime smoke tests - verify binaries work without missing libraries
# Test pcscd
RUN ["/usr/sbin/pcscd", "--version"]
# Test hsm-operator
RUN ["/hsm-operator", "--help"]

# Default to distroless nonroot user (can be overridden by deployment securityContext)
USER 65532:65532

# Stage 3: Debug variant with shell (DEFAULT for testing)
FROM runtime

# Debug image includes busybox shell for troubleshooting
# Access via: kubectl exec -it pod -- /busybox/sh
ENTRYPOINT ["/entrypoint.sh"]