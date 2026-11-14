# syntax=docker/dockerfile:1

# 1. Static export of the Next.js UI. Platform-independent output, so it
# builds once on the native build platform for every target platform.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,id=pnpm,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
ENV NEXT_TELEMETRY_DISABLED=1
RUN pnpm build

# 2. Static Go binary with the export embedded (internal/webui/dist),
# cross-compiled on the native build platform (no emulation).
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS server
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/web/out/ internal/webui/dist/
# Set by `make docker` and the release workflow (handbill --version).
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X github.com/ferrule-io/handbill/internal/version.Version=${VERSION} -X github.com/ferrule-io/handbill/internal/version.Commit=${COMMIT}" \
    -o /out/handbill ./cmd/handbill
# Local storage root, owned by the distroless nonroot user (65532).
RUN mkdir -p /out/data

# 3. Runtime: distroless static, non-root.
FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source=https://github.com/ferrule-io/handbill
COPY --from=server /out/handbill /handbill
COPY --from=server --chown=65532:65532 /out/data /data
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/handbill"]
