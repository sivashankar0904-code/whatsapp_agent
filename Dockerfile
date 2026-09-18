# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first, so a code-only change does not refetch the module cache.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# CGO_ENABLED=0 gives a static binary, which is what lets the final stage be
# scratch. -trimpath and -s -w drop build paths and debug symbols.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/wa ./cmd/whatsapp-agent

# Certificates are the only runtime dependency: the agent makes TLS connections
# to WhatsApp, and to S3 when it is configured for HTTPS.
FROM alpine:3.22 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/wa /wa

# Unprivileged, and nothing is written to disk, so the filesystem can be
# mounted read-only.
USER 65534:65534

EXPOSE 8081
ENTRYPOINT ["/wa"]
