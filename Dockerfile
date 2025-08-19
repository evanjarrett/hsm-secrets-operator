# Production Dockerfile with real PKCS#11 support
# Build the manager and agent binaries with CGO enabled
FROM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH

# Install build dependencies for PKCS#11
RUN apk add --no-cache \
  git \
  gcc \
  g++ \
  make \
  cmake \
  pkgconfig \
  openssl-dev \
  pcsc-lite-dev \
  libusb-dev \
  autoconf \
  automake \
  libtool

# Build sc-hsm-embedded library
RUN cd / && git clone https://github.com/CardContact/sc-hsm-embedded.git 
WORKDIR /sc-hsm-embedded
RUN autoreconf -fi && ./configure
RUN make && make install

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

# Build manager and discovery without CGO (they use mock clients)
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/manager/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o discovery cmd/discovery/main.go

# Build agent with CGO enabled for PKCS#11 support
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o agent cmd/agent/main.go

FROM alpine:3.22
RUN apk add --no-cache opensc-dev ccid pcsc-lite openssl libtool libusb

COPY --from=builder /usr/lib/libssl.so* /usr/lib/
COPY --from=builder /usr/lib/libcrypto.so* /usr/lib/
COPY --from=builder /usr/local/ /usr/local/

WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/agent .
COPY --from=builder /workspace/discovery .
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER 65532:65532

ENTRYPOINT ["/entrypoint.sh"]
