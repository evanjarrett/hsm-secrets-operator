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

# Collect all runtime dependencies using iterative discovery:
# 1. Start with ldd on binaries → get compile-time linked libraries
# 2. Recursively: ldd on discovered libraries → get their dependencies
# 3. strings scan discovered libraries → find dlopen'd libraries (like libgcc_s, libpcsclite_real)
# 4. Repeat step 2-3 on newly discovered libraries until no new deps found
RUN echo "Discovering runtime dependencies (iterative)..." && \
    # Define binaries to scan
    SCAN_BINARIES="/workspace/hsm-operator /usr/sbin/pcscd /usr/bin/pkcs11-tool" && \
    mkdir -p /runtime-deps && \
    touch /tmp/deps_all.txt /tmp/deps_previous.txt /tmp/deps_new.txt && \
    # Start with our binaries
    echo "$SCAN_BINARIES" | tr ' ' '\n' > /tmp/deps_new.txt && \
    ITERATION=0 && \
    while [ -s /tmp/deps_new.txt ] && [ $ITERATION -lt 10 ]; do \
        ITERATION=$((ITERATION + 1)) && \
        NEW_COUNT=$(wc -l < /tmp/deps_new.txt) && \
        echo "Iteration $ITERATION: Processing $NEW_COUNT new items..." && \
        # Run ldd on new items
        for item in $(cat /tmp/deps_new.txt); do \
            if [ -f "$item" ]; then \
                ldd "$item" 2>/dev/null | grep "=>" | awk '{print $3}' | grep -v "^$" || true; \
                # Also get dynamic linker
                ldd "$item" 2>/dev/null | grep -o '/lib.*/ld-linux[^ ]*' || true; \
            fi; \
        done >> /tmp/deps_all.txt && \
        # Scan new items for dlopen'd libraries (strings method)
        for item in $(cat /tmp/deps_new.txt); do \
            if [ -f "$item" ]; then \
                strings "$item" 2>/dev/null | grep -E '\.so(\.[0-9]+)*$' | while read soname; do \
                    find /usr/lib /lib -name "$soname" 2>/dev/null || true; \
                done; \
            fi; \
        done >> /tmp/deps_all.txt && \
        # Find newly discovered deps (not in previous iterations)
        sort -u /tmp/deps_all.txt > /tmp/deps_all_sorted.txt && \
        comm -13 /tmp/deps_previous.txt /tmp/deps_all_sorted.txt > /tmp/deps_new.txt && \
        cp /tmp/deps_all_sorted.txt /tmp/deps_previous.txt; \
    done && \
    # Final deduplication - remove duplicate paths (same inode, different path)
    sort -u /tmp/deps_all.txt > /tmp/deps_sorted.txt && \
    # Deduplicate by basename - prefer /usr/lib over /lib for consistency
    awk '{print $0, $0}' /tmp/deps_sorted.txt | \
        awk -F'/' '{print $(NF), $0}' | \
        sort -t' ' -k1,1 -k2,2r | \
        awk '!seen[$1]++ {print $2}' > /tmp/deps.txt && \
    echo "Found $(wc -l < /tmp/deps.txt) unique library dependencies after $ITERATION iterations ($(wc -l < /tmp/deps_sorted.txt) before deduplication)" && \
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

# Copy all runtime library dependencies (auto-discovered via ldd)
COPY --from=builder /runtime-deps /

# Copy PKCS#11 library (loaded via dlopen by Go app at runtime with user-specified path)
COPY --from=builder /usr/lib/*/opensc-pkcs11.so /usr/lib/pkcs11/

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