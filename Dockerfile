# Build stage: compile a static binary with the UI embedded in it.
FROM golang:1.24-alpine AS build
WORKDIR /src

# atrium has no third-party dependencies, so there is nothing to download here -
# the build is hermetic and offline, and there is no go.sum by design.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/atrium ./cmd/atrium

# Run stage: the binary, CA certificates, and nothing else.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 atrium \
    && mkdir -p /var/lib/atrium \
    && chown atrium:atrium /var/lib/atrium

COPY --from=build /out/atrium /usr/local/bin/atrium

USER atrium
EXPOSE 8080

# The state directory holds the credentials atrium provisions for each backend
# and the single account. Losing it costs a re-provision, not a library.
VOLUME ["/var/lib/atrium"]

ENTRYPOINT ["/usr/local/bin/atrium"]
