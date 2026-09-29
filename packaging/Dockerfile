# d9c container image (published as ghcr.io/kirg0/d9c, linux/amd64 + linux/arm64).
#
#   docker build -f packaging/Dockerfile -t d9c .
#
#   # local daemon through the mounted socket
#   docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock \
#     ghcr.io/kirg0/d9c -H unix:///var/run/docker.sock
#
#   # remote host over SSH with your keys (known_hosts is updated on first connect)
#   docker run --rm -it -v ~/.ssh:/root/.ssh ghcr.io/kirg0/d9c -H ssh://user@host
#
# Config (d9c-config.yaml, d9c-plugins.yaml) lives in the /config volume.

# The Go toolchain always runs natively and cross-compiles for the target, so
# a multi-arch build needs no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS
ARG TARGETARCH
# Release builds stamp the tag's version; empty keeps the one in the source.
ARG VERSION=""
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w ${VERSION:+-X github.com/kirg0/d9c/internal/version.Version=$VERSION}" \
      -o /out/d9c .

# alpine (not scratch/distroless): ships CA certificates and a /bin/sh for
# plugins, and the final stage has no RUN, so it also builds without QEMU.
FROM alpine:3.22
LABEL org.opencontainers.image.title="d9c" \
      org.opencontainers.image.description="Terminal UI for managing Docker on remote hosts over TCP or SSH" \
      org.opencontainers.image.source="https://github.com/kirg0/d9c" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/d9c /usr/local/bin/d9c
ENV TERM=xterm-256color
VOLUME /config
WORKDIR /config
ENTRYPOINT ["/usr/local/bin/d9c", "-config", "/config/d9c-config.yaml", "-plugins-file", "/config/d9c-plugins.yaml"]
