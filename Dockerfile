# Build the manager and agent binaries
FROM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH

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

# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/manager/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o agent cmd/agent/main.go

FROM alpine:3.22 AS base 

# Update Alpine packages
RUN apk update

# Install compilation tools
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

RUN cd / && git clone https://github.com/CardContact/sc-hsm-embedded.git 
WORKDIR /sc-hsm-embedded
RUN autoreconf -fi && ./configure
RUN make && make install

FROM alpine:3.22
RUN apk add --no-cache opensc-dev ccid pcsc-lite openssl libtool libusb

COPY --from=base /usr/lib/libssl.so* /usr/lib/
COPY --from=base /usr/lib/libcrypto.so* /usr/lib/
COPY --from=base /usr/local/ /usr/local/

WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/agent .
USER 65532:65532

ENTRYPOINT ["/manager"]
