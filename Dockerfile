# Multi-stage distroless Dockerfile for maximum security with USB access
# Phase 2: Root + Distroless - compensates for root requirement with minimal attack surface

# Stage 1: Go builder (also serves as dependency source)
FROM golang:1.24-bookworm AS builder
ARG TARGETOS
ARG TARGETARCH

# Install both runtime and development packages in single stage
RUN apt-get update && apt-get install -y \
    opensc \
    pcscd \
    libpcsclite1 \
    libusb-1.0-0 \
    udev \
    ca-certificates \
    libudev-dev \
    libpcsclite-dev \
    libusb-1.0-0-dev \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Extract newer libccid files from Debian Trixie (avoiding dependency conflicts)
RUN echo "Extracting newer libccid 1.6.2 from Debian Trixie..." && \
    wget -q http://deb.debian.org/debian/pool/main/c/ccid/libccid_1.6.2-1_amd64.deb && \
    dpkg-deb -x libccid_1.6.2-1_amd64.deb /tmp/trixie-ccid && \
    echo "Replacing CCID drivers with newer versions..." && \
    cp -r /tmp/trixie-ccid/usr/lib/pcsc/* /usr/lib/pcsc/ 2>/dev/null || true && \
    cp /tmp/trixie-ccid/etc/libccid_Info.plist /etc/ 2>/dev/null || true && \
    cp /tmp/trixie-ccid/usr/lib/udev/rules.d/* /lib/udev/rules.d/ 2>/dev/null || true && \
    rm -rf /tmp/trixie-ccid libccid_1.6.2-1_amd64.deb && \
    echo "✅ CCID 1.6.2 files extracted and installed successfully"

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

# Stage 2: Base runtime stage with all files
FROM gcr.io/distroless/cc-debian12:debug AS runtime

# Copy essential system files and create nonroot user
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Ensure nonroot user exists (distroless provides user 65532:65532)

# Copy PKCS#11 and USB libraries with explicit architecture paths
# Use find to locate the correct architecture-specific paths
COPY --from=builder /usr/lib/*/opensc-pkcs11.so /usr/lib/pkcs11/
COPY --from=builder /usr/lib/*/libopensc.so.8* /usr/lib/
COPY --from=builder /usr/lib/*/libpcsclite.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libusb-1.0.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libudev.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libglib-2.0.so.0* /lib/*/libglib-2.0.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libgio-2.0.so.0* /lib/*/libgio-2.0.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libgobject-2.0.so.0* /lib/*/libgobject-2.0.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libgmodule-2.0.so.0* /lib/*/libgmodule-2.0.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libmount.so.1* /lib/*/libmount.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libselinux.so.1* /lib/*/libselinux.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libffi.so.8* /lib/*/libffi.so.8* /usr/lib/
COPY --from=builder /usr/lib/*/libpcre2-8.so.0* /lib/*/libpcre2-8.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libblkid.so.1* /lib/*/libblkid.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libcap.so.2* /usr/lib/
COPY --from=builder /usr/lib/*/libsystemd.so.0* /lib/*/libsystemd.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libgcrypt.so.20* /lib/*/libgcrypt.so.20* /usr/lib/
COPY --from=builder /usr/lib/*/liblzma.so.5* /lib/*/liblzma.so.5* /usr/lib/
COPY --from=builder /usr/lib/*/libzstd.so.1* /lib/*/libzstd.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/liblz4.so.1* /lib/*/liblz4.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libgpg-error.so.0* /lib/*/libgpg-error.so.0* /usr/lib/
COPY --from=builder /lib/*/libgcc_s.so.1* /usr/lib/
# Copy zlib for pkcs11-tool
COPY --from=builder /lib/*/libz.so.1* /usr/lib/

# Copy essential binaries
COPY --from=builder /usr/sbin/pcscd /usr/sbin/
COPY --from=builder /usr/bin/pkcs11-tool /usr/bin/

# Copy udev rules for HSM devices (CCID support)
COPY --from=builder /lib/udev/rules.d/92-libccid.rules /lib/udev/rules.d/

# Copy CCID drivers for pcscd (now using newer 1.6.2 version)
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

# Default to distroless nonroot user (can be overridden by deployment securityContext)
USER 65532:65532

# Stage 3: Debug variant with shell (DEFAULT for testing)
FROM runtime

# Debug image includes busybox shell for troubleshooting
# Access via: kubectl exec -it pod -- /busybox/sh
ENTRYPOINT ["/entrypoint.sh"]