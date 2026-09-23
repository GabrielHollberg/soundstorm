# The name service, as deployed on Railway. Built from the repository root:
#
#   docker build -f cmd/soundstorm-names/Dockerfile .
#
# Nothing here is SoundStorm itself - that image is the Dockerfile at the root.
# This one is a single static binary answering a handful of requests a day per
# install, which is why it fits in the smallest thing a host will rent.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/soundstorm-names ./cmd/soundstorm-names

FROM alpine:3.20
# ca-certificates for Porkbun's API. The service itself speaks plain HTTP: the
# host's proxy terminates TLS in front of it.
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 names
COPY --from=build /out/soundstorm-names /usr/local/bin/soundstorm-names
USER names
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/soundstorm-names"]
