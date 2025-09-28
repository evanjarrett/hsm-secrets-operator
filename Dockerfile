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
    && rm -rf /var/lib/apt/lists/*

# Create necessary runtime directories
RUN mkdir -p /var/run/pcscd /var/lock/pcsc && \
    chmod 755 /var/run/pcscd /var/lock/pcsc

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
COPY --from=builder /usr/lib/*/libpcsclite.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libusb-1.0.so.0* /usr/lib/
COPY --from=builder /usr/lib/*/libudev.so.1* /usr/lib/
COPY --from=builder /usr/lib/*/libcap.so.2* /usr/lib/
COPY --from=builder /lib/*/libgcc_s.so.1* /usr/lib/

# Copy essential binaries
COPY --from=builder /usr/sbin/pcscd /usr/sbin/
COPY --from=builder /usr/bin/pkcs11-tool /usr/bin/

# Copy udev rules for HSM devices (CCID support)
COPY --from=builder /lib/udev/rules.d/92-libccid.rules /lib/udev/rules.d/

# Copy CA certificates
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy runtime directories
COPY --from=builder /var/run/pcscd /var/run/pcscd
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