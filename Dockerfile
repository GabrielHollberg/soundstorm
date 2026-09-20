# Build stage: compile a static binary with the UI embedded in it.
FROM golang:1.24-alpine AS build
WORKDIR /src

# SoundStorm has no third-party dependencies, so there is nothing to download here -
# the build is hermetic and offline, and there is no go.sum by design.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/soundstorm ./cmd/soundstorm

# Run stage: the binary, CA certificates, and nothing else.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 soundstorm \
    && mkdir -p /var/lib/soundstorm \
    && chown soundstorm:soundstorm /var/lib/soundstorm

COPY --from=build /out/soundstorm /usr/local/bin/soundstorm

USER soundstorm
EXPOSE 8080

# The state directory holds the credentials SoundStorm provisions for each backend
# and the single account. Losing it costs a re-provision, not a library.
VOLUME ["/var/lib/soundstorm"]

ENTRYPOINT ["/usr/local/bin/soundstorm"]
