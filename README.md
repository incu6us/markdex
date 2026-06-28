# markdex

Go 1.26 service that turns Markdown into a searchable knowledge base in
[Qdrant](https://github.com/qdrant/qdrant) (v1.18.x). It **splits each document on `#` H1
headings into separate topics**, embeds every chunk with **BGE-M3** (dense **+** sparse,
1024-dim, 8k context) via a local Python sidecar, and serves **hybrid search with
cross-encoder reranking** over a REST API.

Three containers, each with one job:

- **app** (Go) — chunking, ingestion orchestration, search API, and the embedded React UI.
- **embedder** (Python sidecar) — BGE-M3 embeddings + `bge-reranker-v2-m3` reranking.
- **qdrant** — vector store (dense + sparse named vectors, RRF fusion).

The **React UI** (`web/`) lets you upload a `.md` file or paste a raw GitHub `.md` URL,
preview the H1-topic split, and ingest into a new or existing collection.

## Architecture

```
                              ┌──────────────────────────────┐
  browser ──► :4334 ──────────► app (Go)                     │
  (UI + REST API)             │   • markdown → H1/recursive   │
                              │     chunking                  │
                              │   • ingest + search           │
                              │     orchestration             │
                              │   • embedded React UI         │
                              └───────┬───────────────┬───────┘
                          embed/rerank│               │ upsert / hybrid query
                                      ▼               ▼
                       ┌──────────────────────┐  ┌──────────────────────────┐
                       │ embedder (Python)    │  │ qdrant                   │
                       │  BGE-M3 dense+sparse │  │  dense + sparse vectors  │
                       │  bge-reranker-v2-m3  │  │  RRF fusion              │
                       └──────────────────────┘  └──────────────────────────┘

  ingest:  load → split (H1→H2→…→window) → embed(document) → upsert dense+sparse
  search:  embed(query) → hybrid ANN (dense+sparse, RRF) top-N → rerank → top-k
```

## Quick start

Docker brings up all three services with no native ML setup:

```sh
make docker-up      # build + start app, embedder, qdrant → http://localhost:4334
make docker-logs    # follow logs
make docker-stop    # stop containers (keep them + volumes)
make docker-down    # stop and remove (add ARGS=-v to also drop the data volumes)
```

> First start downloads the BGE-M3 + reranker models (~4.5 GB) into the `hf_cache` volume;
> the app waits for the embedder to finish loading before it serves. Give Docker **≥ 8 GB**
> of memory (the models are held in RAM).

For local development without Docker, run Qdrant + the embedder sidecar in containers and the
Go app on the host:

```sh
make run-qdrant                                   # Qdrant on :6333
docker run -d -p 8000:8000 \
  -v markdex_hf:/root/.cache/huggingface \
  $(docker build -q services/embedder)            # embedder on :8000
make run                                          # build UI + run app → :4334
```

`make run` rebuilds `web/` into `web/dist`, which is **embedded into the Go binary** via
`//go:embed`, and serves both the API and the UI from the same origin on `:4334`. The Go
binary is pure Go (no native dependencies — all ML lives in the sidecar). See
[Run](#run) and [Flags](#flags) below for detail.

### Make targets

| Target | What it does |
|---|---|
| `make run-qdrant` | `docker run -d -p 6333:6333 qdrant/qdrant:v1.18.2` (detached) |
| `make ui-build`   | `npm install` + `npm run build` → `web/dist` |
| `make run`        | `ui-build`, then run the backend serving API + UI |
| `make build`      | `ui-build`, then build one self-contained binary → `bin/markdex-$(GOOS)-$(GOARCH)` |
| `make test`       | `go test ./...` |
| `make docker-build`| `docker compose build app` (rebuild the app image after code changes) |
| `make docker-up`  | `docker compose up --build -d` (app + Qdrant) |
| `make docker-stop`| `docker compose stop` (stop containers, keep them + volumes) |
| `make docker-down`| `docker compose down` (`ARGS=-v` also removes volumes) |
| `make docker-logs`| follow app + Qdrant logs |

Overridable variables: `ADDR` (default `:4334`), `QDRANT_URL`, `QDRANT_VERSION` (default
`v1.18.2`), `GOOS`/`GOARCH`, and `BIN`. Set them inline, e.g.
`make run ADDR=:9000 QDRANT_URL=http://localhost:6333`, or cross-compile with
`make build GOOS=linux GOARCH=amd64`. The Go binary is pure Go with the web UI baked in; it
needs a reachable embedder sidecar (`-embedder`) and Qdrant (`-qdrant`) at runtime.

> For UI development with hot reload, run the backend (`make run` or `go run . -addr :4334`)
> and `cd web && npm run dev` separately — Vite serves on `:5173` and proxies `/api` to `:4334`.

## Docker

`docker-compose.yml` runs three containers — **app** (from the `Dockerfile`), **embedder**
(from `services/embedder/Dockerfile`), and the **official Qdrant image** — each with its own
lifecycle and volumes.

```sh
make docker-up      # http://localhost:4334
make docker-build   # rebuild the app image after code changes (then docker-up to restart)
make docker-stop    # stop containers, keep them and the volumes
make docker-down    # stop and remove containers/network (ARGS=-v also drops volumes)
```

- **app image** — 3-stage build: build the UI (Node) → build the Go binary with the UI
  embedded (`CGO_ENABLED=0`) → `distroless/static`. No native dependencies; tiny image.
- **embedder image** — `python:3.11-slim` + CPU PyTorch + FlagEmbedding hosting BGE-M3 and
  the reranker.
- **networking** — the app reaches the others by compose service name
  (`QDRANT_URL=http://qdrant:6333`, `EMBEDDER_URL=http://embedder:8000`).
- **volumes** — `qdrant_storage` (vector data) and `hf_cache` (the ~4.5 GB models) persist
  across restarts. `make docker-down ARGS=-v` removes them.

## How it works

- **H1 split is structural** — every `#` H1 becomes its own topic. Content before the first
  H1 is a "preamble" topic; a file with no H1 stays a single whole-file chunk.
- **Recursion is size-driven** — within a topic, the splitter only sub-divides when a section
  exceeds the per-request `max_chars` runes: `H2 → H3 → … → sliding window with overlap`. The
  splitter is **code-fence aware** — a `#` inside a ``` ``` block is not treated as a heading.
- **Hybrid embeddings** — each chunk is embedded by BGE-M3 into a **dense** vector (1024-dim,
  cosine) *and* a **sparse** lexical vector. Both are stored as named vectors so search can
  fuse semantic similarity with exact-term matching.
- **Search** — the query is embedded, Qdrant runs a hybrid ANN over dense + sparse and fuses
  the two with **Reciprocal Rank Fusion (RRF)**, then `bge-reranker-v2-m3` reranks the
  candidate pool (default 50) down to the top-k (default 8). Optional metadata filters
  (`source_id` / `title` / `heading_path`) apply at query time.
- **Idempotent re-ingest** — every point carries `metadata.source_id`. Re-ingesting a file
  does *delete-by-`source_id`* then upsert, so changing a document's headings never leaves
  orphaned points behind.
- **Payload** — each point stores
  `{ "document": <chunk text>, "metadata": { "path", "source_id", "title", "heading_path",
  "chunk_index" } }` under named vectors `bge-m3-dense` + `bge-m3-sparse`.

## Prerequisites

`make docker-up` needs only Docker (≥ 8 GB allocated). To run the Go app on the host you need
**Qdrant** and the **embedder sidecar** reachable:

```sh
make run-qdrant                                   # Qdrant on :6333
docker run -d -p 8000:8000 \
  -v markdex_hf:/root/.cache/huggingface \
  $(docker build -q services/embedder)            # embedder on :8000 (downloads models once)
```

## Run

```sh
go run . -addr :4334 -qdrant http://localhost:6333 -embedder http://localhost:8000
```

On startup the app waits for the embedder to report ready (`/healthz`), then reads the model
dimension + vector names from the sidecar's `/info` to size collections.

### Flags

| Flag         | Default                 | Description                                         |
|--------------|-------------------------|-----------------------------------------------------|
| `-addr`      | `:4334`                 | HTTP listen address                                 |
| `-qdrant`    | `http://localhost:6333` | Qdrant REST base URL (`QDRANT_URL` env)             |
| `-embedder`  | `http://localhost:8000` | Embedder sidecar base URL (`EMBEDDER_URL` env)      |
| `-pool`      | `24`                    | Rerank candidate pool (lower = faster, higher = better recall) |

`QDRANT_API_KEY` is read from the environment and sent as the `api-key` header when set.
Per-document `max_chars` and `overlap` are set per ingest request.

### Tuning search latency

Search cost is dominated by the cross-encoder reranker running on CPU (one forward pass per
candidate). The sidecar env knobs (in `docker-compose.yml`) trade speed vs. quality:

| Env (embedder) | Default | Effect |
|---|---|---|
| `RERANK_MODEL` | `cross-encoder/ms-marco-MiniLM-L-6-v2` | The big lever. MiniLM (English, 22M) reranks a 24-pool in ~0.1 s; `BAAI/bge-reranker-v2-m3` (multilingual, 568M) is higher quality but ~55× slower on CPU (~6 s) — prefer it only on a GPU. |
| `RERANK_MAX_LENGTH` | `256` | Tokens per query–doc pair; lower is faster. |
| `USE_FP16` | `true` | fp16 is faster on Apple Silicon (native arm64 fp16); set `false` only if your CPU lacks fp16. |

Plus the app's `-pool` flag (rerank candidate count). With MiniLM, end-to-end search on a
~100-chunk collection is well under a second.

## HTTP API + Web UI

Endpoints:

| Method & path | Purpose |
|---|---|
| `POST /api/preview` | Split a source and return the H1-topic tree (no embedding). |
| `GET /api/collections` | List collections with dimension, named vector, and point count. |
| `POST /api/collections` | Create a collection sized for the embedding model. |
| `POST /api/ingest` | Validate + enqueue an async ingest job → `202 { job_id }`. |
| `POST /api/search` | Hybrid + reranked search → `{ results: [{ id, score, document, metadata }] }`. |
| `GET /api/jobs/{id}` | Job state (`pending`/`running`/`succeeded`/`failed`, progress, count). |
| `GET /api/jobs/{id}/stream` | Server-Sent Events stream of the same job state. |

A request `source` is either `{ "type": "upload", "name", "content" }` or
`{ "type": "github_raw", "url": "https://raw.githubusercontent.com/owner/repo/ref/file.md" }`
(a `github.com/.../blob/...` URL is accepted and rewritten to raw). Ingesting into an
existing collection whose dimension/vector doesn't match the model is rejected with `409`.

Search:

```sh
curl -s -X POST http://localhost:4334/api/search -H 'Content-Type: application/json' -d '{
  "collection": "go-guide",
  "query": "how do I wrap errors in Go?",
  "top_k": 8,
  "filter": { "source_id": "https://raw.githubusercontent.com/owner/repo/main/guide.md" }
}'
```

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

## Retrieval

markdex **owns retrieval** through `POST /api/search` (hybrid dense+sparse → RRF → rerank),
so the quality of ranking is under its control rather than delegated. Use it from your agent
or app over plain HTTP.

> The stock [Qdrant MCP server](https://github.com/qdrant/mcp-server-qdrant) (`qdrant-find`)
> is **not** compatible with these collections: it does plain dense kNN with its own model,
> while markdex stores BGE-M3 dense **and** sparse vectors under `bge-m3-dense` /
> `bge-m3-sparse` and reranks. Surfacing the reranked `/api/search` path to agents as an MCP
> tool is tracked in [`docs/roadmap.md`](docs/roadmap.md).

## Architecture (DDD / hexagonal)

Ports & adapters — the domain and application layers depend only on interfaces
(`domain.DocumentSource`, `domain.Chunker`, `domain.Embedder`, `domain.Reranker`,
`domain.VectorRepository`); infrastructure provides the adapters, wired in `main.go`.

```
main.go                                       composition root (HTTP API server)

internal/domain/                              the model + ubiquitous language
  document.go / chunk.go                      Document, Chunk value objects
  embedding.go / sparse_embedding.go          Embedding, SparseEmbedding; Vectors = {Dense, Sparse}
  embedded_chunk.go / search.go               EmbeddedChunk; CollectionSchema, Filter, SearchHit
  ports.go / rerank.go / embed_kind.go        DocumentSource / Chunker / Embedder / Reranker / VectorRepository

internal/application/
  ingest.go                                   IngestService (Prepare → Load → Split → Embed → Replace)
  search.go                                   SearchService (embed query → hybrid search → rerank)

internal/infrastructure/
  github/fetcher.go                           fetches raw .md from GitHub (blob → raw)
  markdown/splitter.go                        Chunker: recursive, code-fence-aware H1 splitter
  embedderclient/                             HTTP client to the embedder sidecar (Embedder + Reranker)
  qdrant/repository.go                        VectorRepository: hybrid Prepare/Replace/Search/List
  httpapi/                                    HTTP server: preview / collections / ingest / search / jobs (+ SSE)

services/embedder/                            Python sidecar: BGE-M3 + bge-reranker-v2-m3
web/                                          React (Vite) UI for the HTTP API
```

## Tests (TDD)

Unit tests are written before the implementation and run against in-memory fakes /
`httptest` servers — no models or running services required.

```
internal/domain/*_test.go                     value objects (Document, Chunk, SparseEmbedding, EmbedKind)
internal/application/                         IngestService + SearchService (fakes for every port)
internal/infrastructure/markdown/             splitter behaviour (H1, recursion, fences, windows)
internal/infrastructure/github/               URL normalization + fetch (httptest)
internal/infrastructure/embedderclient/       sidecar contract: embed / rerank / info (httptest)
internal/infrastructure/qdrant/               hybrid wire contract: prepare/replace/search/list (httptest)
internal/infrastructure/httpapi/              handlers + async job lifecycle (httptest)
```

```sh
go test ./...                 # all unit tests
go vet ./...
```

## Retrieval evaluation

`cmd/eval` measures retrieval quality against a golden query set: it posts each query to a
running `/api/search`, checks whether the expected section is retrieved and how highly it
ranks, and reports **MRR / Hit@1 / Hit@3 / Hit@k**. Use it to catch regressions and compare
configs (reranker model, `-pool`, etc.).

```sh
make eval-seed   # ingest the vendored fixture into the collection, then eval (from empty)
make eval        # eval an already-ingested collection
make eval GOLDEN=path.json   # custom golden set
```

`eval-seed` makes the harness reproducible from an empty Qdrant: it ingests the **pinned**
`cmd/eval/golden/go-style-guide.md` (a vendored copy of Google's Go style guide, Apache-2.0)
into the collection first. A golden set is JSON:
`{ collection, top_k, queries: [{ query, relevant_heading_contains }] }` — a result counts as
relevant if its `heading_path` contains one of the substrings.

## Roadmap

markdex is a solid ingestion + storage pipeline today. The gaps to make it a robust
knowledge base for AI agents — a retrieval layer it controls (hybrid + reranking), a
stronger embedder, broader sources, and production hardening — are tracked in
[`docs/roadmap.md`](docs/roadmap.md), checked off as they ship.
