# Build stage: compile a static binary with the UI embedded in it.
#
# --platform=$BUILDPLATFORM pins the builder to the machine doing the building,
# and the target comes from TARGETOS/TARGETARCH instead. A lot of home servers
# are ARM - a Pi, a Synology, an Apple silicon Mac - so the published image is
# multi-arch, and without this the arm64 build would run the whole Go toolchain
# under QEMU emulation. Go cross-compiles natively, so it does not have to.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# SoundStorm has no third-party dependencies, so there is nothing to download here -
# the build is hermetic and offline, and there is no go.sum by design.
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/soundstorm ./cmd/soundstorm

# Run stage: the binary, CA certificates, and nothing else.
FROM alpine:3.20
# /library is created here, owned by the account the server runs as.
#
# Compose bind-mounts a host folder over it, so this changes nothing there -
# but without it a plain `docker run` dies with "mkdir /library: permission
# denied", because a non-root process cannot create a directory at the
# filesystem root. An image that will not start by itself is a poor first
# impression for anybody trying the project out.
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 soundstorm \
    && mkdir -p /var/lib/soundstorm /library \
    && chown soundstorm:soundstorm /var/lib/soundstorm /library

COPY --from=build /out/soundstorm /usr/local/bin/soundstorm

# Labels are what make the image show up with a description and a link back to
# the source on GitHub's package page, which is the first thing anyone
# installing this sees.
LABEL org.opencontainers.image.title="SoundStorm" \
      org.opencontainers.image.description="One login and one search box over your whole media library." \
      org.opencontainers.image.source="https://github.com/GabrielHollberg/soundstorm" \
      org.opencontainers.image.licenses="MIT"

USER soundstorm
EXPOSE 8080

# The state directory holds the credentials SoundStorm provisions for each backend
# and the single account. Losing it costs a re-provision, not a library.
VOLUME ["/var/lib/soundstorm"]

ENTRYPOINT ["/usr/local/bin/soundstorm"]
