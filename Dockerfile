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
RUN CGO_ENABLED=0 go build -trimpath -o /out/markdex .

# ---- runtime (pure-Go static binary; embeddings live in the sidecar) ----
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/markdex /markdex
EXPOSE 4334
ENTRYPOINT ["/markdex"]
CMD ["-addr", ":4334"]
