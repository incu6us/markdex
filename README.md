# markdex

Go 1.26 service that ingests Markdown into [Qdrant](https://github.com/qdrant/qdrant)
(v1.18.x). It **splits each document on `#` H1 headings into separate topics**, embeds
every chunk **locally** with [FastEmbed](https://github.com/anush008/fastembed-go)
(`BAAI/bge-small-en-v1.5`, 384-dim), and upserts them over the REST API.

It runs as an **HTTP API** with a **React UI** (`web/`): upload a `.md` file or paste a raw
GitHub `.md` URL, preview the H1-topic split, and ingest into a new or existing collection.

## Quick start

The fastest path is Docker — it brings up the app **and** Qdrant with no native setup:

```sh
make docker-up      # build the app image + start app and Qdrant → http://localhost:4334
make docker-logs    # follow app + Qdrant logs
make docker-stop    # stop containers (keep them + volumes)
make docker-down    # stop and remove (add ARGS=-v to also drop the data volumes)
```

Or run it natively (requires the ONNX Runtime native library):

```sh
# 1. native dependency for FastEmbed
brew install onnxruntime
export ONNX_PATH=/opt/homebrew/lib/libonnxruntime.dylib

# 2. start Qdrant
make run-qdrant

# 3. build the UI and run the backend (which serves the UI), in another terminal
make run            # → http://localhost:4334
```

`make run` rebuilds `web/` into `web/dist`, which is **embedded into the Go binary** via
`//go:embed`, and starts the server — it serves both the API and the UI from the same origin
on `:4334`. The resulting binary is self-contained (no `web/dist` needed at runtime). See
[Prerequisites](#prerequisites), [Run](#run), and [Flags](#flags) below for detail.

### Make targets

| Target | What it does |
|---|---|
| `make run-qdrant` | `docker run -d -p 6333:6333 qdrant/qdrant:v1.18.2` (detached) |
| `make ui-build`   | `npm install` + `npm run build` → `web/dist` |
| `make run`        | `ui-build`, then run the backend serving API + UI |
| `make build`      | `ui-build`, then build one self-contained binary → `bin/markdex-$(GOOS)-$(GOARCH)` |
| `make test`       | `go test ./...` |
| `make docker-up`  | `docker compose up --build -d` (app + Qdrant) |
| `make docker-stop`| `docker compose stop` (stop containers, keep them + volumes) |
| `make docker-down`| `docker compose down` (`ARGS=-v` also removes volumes) |
| `make docker-logs`| follow app + Qdrant logs |

Overridable variables: `ADDR` (default `:4334`), `QDRANT_URL`, `QDRANT_VERSION` (default
`v1.18.2`), `ONNX_PATH`, `GOOS`/`GOARCH`, and `BIN`. Set them inline, e.g.
`make run ADDR=:9000 QDRANT_URL=http://localhost:6333`, or
cross-compile with `make build GOOS=linux GOARCH=amd64`. The built binary still needs the
ONNX Runtime shared library available at runtime (`ONNX_PATH`); everything else — including
the web UI — is baked in.

> For UI development with hot reload, run the backend (`make run` or `go run . -addr :4334`)
> and `cd web && npm run dev` separately — Vite serves on `:5173` and proxies `/api` to `:4334`.

## Docker

`docker-compose.yml` runs two containers: the **app** (built from the `Dockerfile`) and the
**official Qdrant image** — kept separate so each has its own lifecycle and persistent volume.

```sh
make docker-up      # http://localhost:4334
make docker-stop    # stop containers, keep them and the volumes
make docker-down    # stop and remove containers/network (ARGS=-v also drops volumes)
```

- **App image** — a 3-stage build: build the UI (Node) → build the Go binary with the UI
  embedded → a slim Debian runtime with the ONNX Runtime shared library installed (matched to
  the image architecture). `ONNX_PATH` is preset, so the container is fully self-contained.
- **Networking** — the app reaches Qdrant via the compose service name (`QDRANT_URL=http://qdrant:6333`).
- **Volumes** — `qdrant_storage` (vector data) and `model_cache` (the ~77 MB embedding model,
  downloaded on the first ingest) both persist across restarts. `make docker-down ARGS=-v`
  removes them.

## How it works

```
*.md ──► split on H1 (recursive) ──► FastEmbed (ONNX, local) ──► 384-d vectors ──► Qdrant upsert
```

- **H1 split is structural** — every `#` H1 becomes its own topic. Content before the first
  H1 is a "preamble" topic; a file with no H1 stays a single whole-file chunk.
- **Recursion is size-driven** — within a topic, the splitter only sub-divides when a section
  exceeds the per-request `max_chars` runes (the 512-token guard): `H2 → H3 → … → sliding
  window with overlap`. Every emitted chunk is guaranteed to fit the model window. The
  splitter is **code-fence aware** — a `#` inside a ``` ``` block is not treated as a heading.
- **Idempotent re-ingest** — every point carries `metadata.source_id`. Re-ingesting a file
  does *delete-by-`source_id`* then upsert, so changing a document's headings never leaves
  orphaned points behind.
- The collection is written in the layout the [official Qdrant MCP server](https://github.com/qdrant/mcp-server-qdrant)
  expects, so the data is searchable from Claude Code with no extra steps
  (see [Use the ingested data in Claude Code](#use-the-ingested-data-in-claude-code)):
  - **named vector** `fast-bge-small-en-v1.5` (the server's `fast-<model>` convention),
  - payload `{ "document": <chunk text>, "metadata": { "path", "source_id", "title",
    "heading_path", "chunk_index" } }`.

> **Note:** `bge-small-en-v1.5` has a hard 512-token input window. Chunking keeps sections
> under `max_chars` runes (default 2000 ≈ ~450 tokens); as a final safety net the embedder
> still truncates anything longer. (The truncation lives in the embedder because the bundled
> tokenizer's own truncation is broken — see the comment in
> `internal/infrastructure/fastembed/embedder.go`.)

## Prerequisites

1. **ONNX Runtime** native library (FastEmbed runs the model through it):
   ```sh
   brew install onnxruntime
   export ONNX_PATH=/opt/homebrew/lib/libonnxruntime.dylib
   ```
   On Linux: download from the [onnxruntime releases](https://github.com/microsoft/onnxruntime/releases)
   and point `ONNX_PATH` at `libonnxruntime.so`.

2. **Qdrant** running locally:
   ```sh
   docker run -p 6333:6333 -p 6334:6334 qdrant/qdrant:v1.18.2
   ```

## Run

```sh
ONNX_PATH=/opt/homebrew/lib/libonnxruntime.dylib \
  go run . -addr :4334 -qdrant http://localhost:6333
```

First run downloads the embedding model (~77 MB) into `-cache` (default `model_cache`).

### Flags

| Flag           | Default                 | Description                                  |
|----------------|-------------------------|----------------------------------------------|
| `-addr`        | `:4334`                 | HTTP listen address                          |
| `-qdrant`      | `http://localhost:6333` | Qdrant REST base URL (`QDRANT_URL` env)      |
| `-cache`       | `model_cache`           | Embedding model cache directory              |

`QDRANT_API_KEY` is read from the environment and sent as the `api-key` header when set.
Per-document `max_chars` and `overlap` are set per ingest request (see the API below).

## HTTP API + Web UI

Endpoints:

| Method & path | Purpose |
|---|---|
| `POST /api/preview` | Split a source and return the H1-topic tree (no embedding). |
| `GET /api/collections` | List collections with dimension, named vector, and point count. |
| `POST /api/collections` | Create a collection sized for the embedding model. |
| `POST /api/ingest` | Validate + enqueue an async ingest job → `202 { job_id }`. |
| `GET /api/jobs/{id}` | Job state (`pending`/`running`/`succeeded`/`failed`, progress, count). |
| `GET /api/jobs/{id}/stream` | Server-Sent Events stream of the same job state. |

A request `source` is either `{ "type": "upload", "name", "content" }` or
`{ "type": "github_raw", "url": "https://raw.githubusercontent.com/owner/repo/ref/file.md" }`
(a `github.com/.../blob/...` URL is accepted and rewritten to raw). Ingesting into an
existing collection whose dimension/vector doesn't match the model is rejected with `409`.

The React UI lives in `web/` (Vite). In production it is built into `web/dist` and served by
the Go backend from the same origin (`make run`, or `make ui-build` + `go run .`). For hot
reload during development, run `cd web && npm run dev` (Vite on `:5173`, proxying `/api` to
`:4334`).

Flow: pick a source (upload or GitHub URL) → preview the H1 topics → choose a new or
existing collection → ingest with a live progress bar.

## Verify

```sh
curl -s http://localhost:6333/collections/markdown | jq .result.points_count
```

## Use the ingested data in Claude Code

Claude Code reads the collection through the official
[Qdrant MCP server](https://github.com/qdrant/mcp-server-qdrant), which exposes a
`qdrant-find` tool for semantic search. This ingester writes the collection in exactly the
layout that server reads (named vector `fast-bge-small-en-v1.5`, `document` + `metadata`
payload), so no conversion is needed.

> **Critical:** the MCP server must embed queries with the **same model** used for ingestion.
> Set `EMBEDDING_MODEL=BAAI/bge-small-en-v1.5`. If you leave it at the server's default
> (`all-MiniLM-L6-v2`), query vectors land in a different space / under a different vector
> name and `qdrant-find` returns nothing.

### 1. Register the MCP server

Requires [`uv`](https://docs.astral.sh/uv/) (provides `uvx`). From the repo (or anywhere):

```sh
claude mcp add qdrant \
  -e QDRANT_URL=http://localhost:6333 \
  -e COLLECTION_NAME=markdown \
  -e EMBEDDING_MODEL=BAAI/bge-small-en-v1.5 \
  -e QDRANT_READ_ONLY=true \
  -- uvx mcp-server-qdrant
```

- `QDRANT_READ_ONLY=true` exposes only `qdrant-find` (search), not `qdrant-store` — drop it
  if you also want Claude to write memories into the collection.
- Add `-e QDRANT_API_KEY=...` if your Qdrant requires auth.
- Add `-s project` to write the config to a shared `.mcp.json` instead of your user scope.

Equivalent `.mcp.json` (project scope):

```json
{
  "mcpServers": {
    "qdrant": {
      "command": "uvx",
      "args": ["mcp-server-qdrant"],
      "env": {
        "QDRANT_URL": "http://localhost:6333",
        "COLLECTION_NAME": "markdown",
        "EMBEDDING_MODEL": "BAAI/bge-small-en-v1.5",
        "QDRANT_READ_ONLY": "true"
      }
    }
  }
}
```

### 2. Confirm it is connected

```sh
claude mcp list          # should show "qdrant: ... - ✓ Connected"
```

Inside a Claude Code session, `/mcp` lists the server and its `qdrant-find` tool.

### 3. Ask questions against your markdown

Claude calls `qdrant-find` automatically when a prompt needs the indexed docs. Examples:

```
> Search my notes for what Qdrant is used for.
> Using the qdrant-find tool, what does the markdown say about Go 1.26?
```

The tool returns the matching `document` text plus its `metadata.path`, which Claude then
uses to answer — a minimal RAG loop over your `*.md` files.

> First call downloads the model on the Python side too (FastEmbed pulls
> `bge-small-en-v1.5`), so the first `qdrant-find` may take a few seconds.

## Architecture (DDD / hexagonal)

Ports & adapters — the domain and application layers depend only on interfaces
(`domain.DocumentSource`, `domain.Chunker`, `domain.Embedder`, `domain.VectorRepository`);
the infrastructure layer provides the concrete adapters, wired together in `main.go`.

```
main.go                                       composition root (HTTP API server)

internal/domain/                              the model + ubiquitous language
  document.go                                 Document value object (path identity, UUIDv5 ID)
  chunk.go                                    Chunk value object (source_id + index identity)
  embedding.go                                Embedding value object
  embedded_chunk.go                           EmbeddedChunk
  ports.go                                    DocumentSource / Chunker / Embedder / VectorRepository

internal/application/
  ingest.go                                   IngestService use case (Prepare → Load → Split → Embed → Replace)

internal/infrastructure/
  github/fetcher.go                           fetches raw .md from GitHub (blob → raw)
  markdown/splitter.go                        Chunker: recursive, code-fence-aware H1 splitter
  fastembed/embedder.go                       Embedder: local ONNX (bge-small-en-v1.5)
  qdrant/repository.go                        VectorRepository: REST, idempotent Prepare + Replace + List
  httpapi/                                    HTTP server: preview / collections / ingest / jobs (+ SSE)

web/                                          React (Vite) UI for the HTTP API
```

## Tests (TDD)

Unit tests are written before the implementation and run against in-memory fakes /
`httptest` servers — no ONNX or running Qdrant required.

```
internal/domain/*_test.go                     value-object unit tests (Document, Chunk)
internal/application/ingest_test.go           IngestService (table-driven, fakes for every port)
internal/infrastructure/markdown/             splitter behaviour (H1, recursion, fences, windows)
internal/infrastructure/github/               URL normalization + fetch (httptest)
internal/infrastructure/qdrant/               Qdrant wire contract (delete-then-upsert, list)
internal/infrastructure/httpapi/              handlers + async job lifecycle (httptest)
```

```sh
go test ./...                 # all unit tests
go vet ./...
```
