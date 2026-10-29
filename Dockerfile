# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.27.3-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/app ./cmd/app \
 && mkdir -p /out/data/export

# ---- runtime ----
# distroless/static: no shell, no package manager, runs as uid 65532 (nonroot).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
# Pre-create /data owned by nonroot so a fresh named volume inherits the ownership.
COPY --from=build --chown=65532:65532 /out/data /data
ENV APP_LISTEN=:8080 \
    APP_DB_PATH=/data/app.db \
    APP_EXPORT_DIR=/data/export \
    APP_TIMEZONE=Asia/Phnom_Penh
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app", "healthcheck"]
ENTRYPOINT ["/app"]
CMD ["serve"]
