# Stage 1: Go builder
FROM golang:1.26.4-trixie AS builder
ARG TARGETOS
ARG TARGETARCH

# Build-only dependency: the sole external lib the CGO build links is libudev
# (via go-udev). The pkcs11 CGO path vendors its header and dlopen's the module
# at runtime, so it needs no -dev package. gcc/git come from the golang base.
# (Runtime libs — opensc/pcscd/libccid/etc — live in the final stage, not here.)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libudev-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Create minimal /etc/passwd and /etc/group for nonroot user (65532:65532)
RUN echo "nonroot:x:65532:65532:nonroot:/:" > /tmp/passwd && \
    echo "nonroot:x:65532:" > /tmp/group

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
# Strip debug symbols to reduce binary size (-s -w)
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -tags udev -ldflags="-s -w" -o hsm-operator cmd/hsm-operator/main.go

# Build test utility for manual testing/debugging
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -tags udev -ldflags="-s -w" -o test cmd/test/main.go

# Stage 2: Debian Forky Slim (minimal but functional for USB hardware interaction)
# Provides proper runtime environment for libudev USB device enumeration.
# forky (testing) ships CCID 1.8.2, whose Info.plist allowlist natively includes
# the new Pico HSM USB ID 2e8a:10fd (added upstream in CCID 1.8.0). Trixie/stable
# only has CCID 1.6.2, which does not know that ID, so pcscd there cannot drive a
# Pico HSM running current firmware. The small runtime package set (opensc, pcscd,
# libccid, libpcsclite1, libusb-1.0-0) makes tracking testing low-risk here.
FROM debian:forky-slim

# Install only the essential runtime packages (minimal attack surface)
RUN apt-get update && apt-get install -y --no-install-recommends \
    opensc \
    pcscd \
    libccid \
    libpcsclite1 \
    libusb-1.0-0 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy minimal user/group files for nonroot user (secure by default)
COPY --from=builder /tmp/passwd /etc/passwd
COPY --from=builder /tmp/group /etc/group

# Create runtime directories for pcscd with proper permissions
# Agent mode requires root for USB device access (standard for HSM/smartcard ops)
RUN mkdir -p /run/pcscd /var/lock/pcsc && \
    chmod 755 /run/pcscd /var/lock/pcsc

# Copy application binary (manages pcscd lifecycle internally - no shell needed)
COPY --from=builder /workspace/hsm-operator /hsm-operator

# Copy test utility for debugging and manual testing
COPY --from=builder /workspace/test /test

# Default to nonroot user for security (manager/discovery modes don't need root)
# Agent mode overrides to root via Kubernetes securityContext for USB device access
USER 65532:65532

# Direct binary execution - pcscd lifecycle managed by Go code in agent mode
ENTRYPOINT ["/hsm-operator"]
