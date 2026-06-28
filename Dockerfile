# syntax=docker/dockerfile:1

# ---- build the web UI ----
FROM node:22-slim AS ui
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- build the Go binary (UI embedded via //go:embed) ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /web/dist ./web/dist
RUN CGO_ENABLED=1 go build -trimpath -o /out/markdex .

# ---- runtime ----
FROM debian:bookworm-slim
ARG ONNXRUNTIME_VERSION=1.20.1
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl libgomp1 \
 && arch="$(dpkg --print-architecture)" \
 && case "$arch" in \
      amd64) ort=x64 ;; \
      arm64) ort=aarch64 ;; \
      *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
    esac \
 && curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-${ort}-${ONNXRUNTIME_VERSION}.tgz" -o /tmp/ort.tgz \
 && mkdir -p /opt/onnxruntime \
 && tar -xzf /tmp/ort.tgz -C /opt/onnxruntime --strip-components=1 \
 && rm /tmp/ort.tgz \
 && apt-get purge -y curl && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

ENV ONNX_PATH=/opt/onnxruntime/lib/libonnxruntime.so
WORKDIR /app
COPY --from=build /out/markdex /usr/local/bin/markdex

EXPOSE 4334
ENTRYPOINT ["markdex"]
CMD ["-addr", ":4334"]
