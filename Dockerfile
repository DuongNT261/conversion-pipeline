# Multi-stage build (docs/01 section 9.2): one image with every service binary;
# each compose service picks its binary with entrypoint.

# Build stage: the Go toolchain, about 1 GB, never shipped.
FROM golang:1.27-bookworm AS build
WORKDIR /src

# Modules get their own layer, so editing code does not download them again.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off gives static binaries, which run on an image with no C library.
RUN CGO_ENABLED=0 go build -o /app/ ./cmd/importer ./cmd/relay ./cmd/worker ./cmd/report

# Run stage: only the binaries. No shell, no package manager, a few MB.
FROM gcr.io/distroless/static-debian12
COPY --from=build /app/ /app/
USER nonroot:nonroot
