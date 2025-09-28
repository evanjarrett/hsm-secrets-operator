# Production Dockerfile with real PKCS#11 support
# Build the manager and agent binaries with CGO enabled
FROM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH

# Install build dependencies for PKCS#11 and USB event monitoring
RUN apk add --no-cache \
  gcc \
  g++ \
  eudev-dev \
  linux-headers

# Return to workspace for Go builds
WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
COPY web/ web/

# Build unified binary with CGO enabled for PKCS#11 support (agent mode needs it)
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o hsm-operator cmd/hsm-operator/main.go

FROM alpine:3.22
RUN apk add --no-cache opensc-dev ccid pcsc-lite openssl libtool libusb ca-certificates eudev polkit

WORKDIR /
COPY --from=builder /workspace/hsm-operator .
COPY --from=builder /workspace/web ./web/
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER 65532:65532

ENTRYPOINT ["/entrypoint.sh"]
