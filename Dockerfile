# syntax=docker/dockerfile:1

# ---- build stage -----------------------------------------------------------
# Runs on the build host's native platform and cross-compiles for the target,
# which keeps multi-arch builds fast (no QEMU emulation of the Go compiler).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# Download modules first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/uptime-pulse ./cmd/uptime-pulse

# ---- runtime stage ---------------------------------------------------------
# distroless/static: no shell, no package manager, CA certificates included.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/uptime-pulse /usr/local/bin/uptime-pulse

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/uptime-pulse"]
CMD ["-config", "/etc/uptime-pulse/config.yaml"]
