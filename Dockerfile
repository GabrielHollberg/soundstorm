# Build stage: compile a static binary.
FROM golang:1.24-alpine AS build
WORKDIR /src

# atrium has no third-party dependencies, so there is nothing to download
# here - the build is hermetic and offline.
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/atrium ./cmd/atrium

# Run stage: the binary, CA certificates (upstreams are HTTPS) and nothing else.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 atrium
COPY --from=build /out/atrium /usr/local/bin/atrium
USER atrium
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/atrium"]
CMD ["-config", "/etc/atrium/atrium.json"]
