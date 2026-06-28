# Initial Plan — Markdown → Qdrant Ingestion App

A Go backend + React frontend that ingests Markdown into Qdrant, splitting each
document on `#` H1 headers into separate topics. Sources can be local `*.md`
files or raw `*.md` files from GitHub. The user chooses whether a document
becomes its own collection or is added to an existing one.

This document is the single source of truth for scope, decisions, architecture,
and remaining work. The app ships as a **single self-contained binary**: a Go HTTP
server that embeds the React UI (`//go:embed`) and serves it alongside the API from
one origin. (The original directory-scanning CLI was removed — see
[Post-completion refinements](#post-completion-refinements).)

## Decisions (locked)

| Topic | Decision |
|---|---|
| Oversized H1 sections (>512 tokens) | **Recursive sub-chunking** — split H1 → H2 → … → sliding window with overlap. No data loss. |
| GitHub ingestion granularity | **Single raw `.md` URL** first (`raw.githubusercontent.com`); repo/folder later. |
| Scope | **Full vision in one pass** — files + GitHub + H1 split + collection management + UI. |
| Testing | **TDD** — unit tests before implementation. No BDD/godog (removed). |
| Style | **Google Go style guide** + Clean Architecture / DDD, small interfaces, explicit error wrapping, no narration comments. |

## Architecture

Clean Architecture with the existing ports as the seams:

```
DocumentSource ──► Chunker ──► Embedder ──► VectorRepository
 (uploaded file,   (markdown    (FastEmbed,   (Qdrant REST:
  github raw)       splitter)    warm model)   delete-by-source + upsert)
```

- **`domain`** — `Document`, `Chunk`, `Embedding`, `EmbeddedChunk`, and the
  ports `DocumentSource`, `Chunker`, `Embedder`, `VectorRepository`.
- **`application`** — `IngestService` orchestrates `Load → Split → Embed → Replace`.
- **`infrastructure`** — `markdown` (splitter), `github` (raw fetcher), `fastembed`,
  `qdrant`, `httpapi` (server).
- **`main`** — the HTTP API server. It loads the embedding model **once** (warm) and
  resolves each request's source (uploaded content or GitHub raw URL) into a `Document`.

### Chunking

- **H1 split is structural** — every `#` H1 becomes a topic, regardless of size.
  Content before the first H1 is a "preamble" topic; a document with no H1 is a
  single whole-file chunk.
- **Recursion is size-driven** — within a topic, only sub-split when the section
  exceeds `maxRunes` (the 512-token guard): H2 → H3 → … → sliding window with
  overlap. Every emitted chunk is guaranteed `≤ maxRunes`.
- **Code-fence aware** — a `#` inside a ``` ``` block is not treated as a heading.
- Hand-rolled ATX splitter (no new deps) behind the `domain.Chunker` port, so it
  can be swapped for goldmark if full CommonMark fidelity is ever needed.

### Identity & idempotency

- `Chunk.ID = UUIDv5(sourceID + "#" + index)` — stable and unique per source.
- Every point carries `metadata.source_id`. Re-ingest does **delete-by-`source_id`
  then upsert**, so changed headings never leave orphaned points.
- Payload stays MCP-compatible: `{ document, metadata }`, where metadata is
  `{ path, source_id, title, heading_path, chunk_index }` under the named vector
  `fast-bge-small-en-v1.5`.

## HTTP API

| Endpoint | Purpose |
|---|---|
| `POST /api/preview` | Parse content, return the H1-topic tree (title, heading_path, chunk count, chars). **No embedding** — cheap, drives the UI preview. |
| `GET /api/collections` | List Qdrant collections with dimension, named vector, and point count (for new-vs-existing + match validation). |
| `POST /api/collections` | Create a collection sized for the embedding model. |
| `POST /api/ingest` | Validate (incl. dim/model match → `409`), enqueue an async job, return `202 { job_id }`. |
| `GET /api/jobs/{id}` | Job state: `pending` / `running` / `succeeded` / `failed` (+ progress, ingested count, error). |
| `GET /api/jobs/{id}/stream` | Server-Sent Events stream of the same job state. |
| `GET /` (+ assets) | The embedded React UI (SPA fallback to `index.html`). |

- Async ingest runs through a **single-worker `JobManager`** (the embedder is not
  concurrency-safe); per-batch progress is reported into the job for the SSE stream.
- Request source shape: `{ type: "upload" | "github_raw", ... }` — `upload`
  carries `{ name, content }`; `github_raw` carries `{ url }`.

## Frontend (React)

Single-page flow:

1. **Source picker** — drop a local `.md` file **or** paste a raw GitHub URL.
2. **Preview pane** — tree of H1 topics with per-topic chunk counts; checkboxes
   to include/exclude and editable titles before committing.
3. **Collection selector** — existing collection (dropdown, with dimension/model
   match check) **or** new collection (name).
4. **Run** — submit ingest, subscribe to the SSE job stream, show a live progress bar.
5. **Result** — ingested chunk count (and the collection list refreshes).

## Build order & status

- [x] **Step 0** — Remove godog/BDD → table-driven unit tests; `go mod tidy`.
- [x] **Step 1** — `Chunk` value object + recursive `markdown.Splitter` (TDD).
- [x] **Step 2** — Wire chunker into the pipeline; `VectorRepository.Replace`
      (delete-by-source + upsert); enriched payload (TDD).
- [x] **Step 3** — `httpapi` server (preview / collections / ingest / jobs) +
      single-worker `JobManager`; warm model; `main -serve`; Qdrant `List` (TDD).
- [x] **Step 4** — `github.Fetcher` (`raw.githubusercontent.com` fetch with ctx +
      timeout, `/blob/`→raw transform); `github_raw` branch in preview/ingest (TDD).
- [x] **Step 5** — React SPA in `web/` (source picker → preview tree → collection
      selector + create → ingest with live SSE progress). Builds clean via Vite.

### Deferred refinements

- [x] **SSE progress** — `GET /api/jobs/{id}/stream`; `IngestService.WithProgress`
      reports per-batch progress into the job (`processed`/`total`).
- [x] **Collection dim/model pre-check** — ingest into a mismatched collection is
      rejected with `409` at request time.
- [x] **`POST /api/collections`** — explicit create endpoint (sized for the model).

### Cross-cutting

- [x] **Docs** — README updated for chunking, `-serve`/`-overlap`, the new payload
      fields, the HTTP API, and the web UI.
- [x] **End-to-end smoke test** — verified against live ONNX + Qdrant: CLI ingest
      (22 chunks, correct metadata), idempotent re-ingest (stayed 22), and the full
      HTTP flow (preview, ingest job with SSE progress, `409` dim-mismatch guard).
- [ ] **Commits** — suggested boundaries: step 0 / chunker / pipeline / server /
      github+UI. (Held — on `main`; needs a branch first.)

## Post-completion refinements

Changes made after the five steps above landed:

- [x] **CLI removed** — the directory-scanning ingest path and the `filesystem`
      package are gone. The app is server-only; the `DocumentSource` port now has a
      single in-memory adapter (one uploaded/fetched doc) feeding `IngestService`.
- [x] **Embedded UI** — `web/dist` is embedded via `//go:embed all:web/dist` and
      served from an `fs.FS` (`http.FileServerFS` + SPA fallback), API routes keep
      precedence. The binary is self-contained (no `web/dist` on disk at runtime).
- [x] **Makefile** — `run-qdrant`, `ui-build`, `run`, `build`, `test`. `build`
      produces one OS/arch-specific binary (`bin/markdex-$(GOOS)-$(GOARCH)`)
      with the UI baked in; cross-compile via `make build GOOS=… GOARCH=…`.
- [x] **Default port** — `:4334` (was `:8080`, which is too generic); Vite dev proxy
      points at `:4334`.

### Possible follow-ups (not in scope)

- GitHub whole-repo/folder ingestion (currently single raw `.md` URL).
- Per-topic include/exclude + title editing in the preview before ingest.
- Authentication on the HTTP API.
- Qdrant data persistence (`run-qdrant` has no volume mount).

## Testing strategy

- TDD: failing unit test first, then the implementation.
- Table-driven tests with `t.Run` + `t.Parallel()`; hand-written fakes for ports.
- `httptest.Server` for the Qdrant wire contract (delete-then-upsert ordering,
  payload shape, nested `metadata.source_id` filter, collection listing).
- `httptest` request/recorder for the HTTP handlers; the job lifecycle is polled
  to a terminal state.
- Native-dependency code (FastEmbed/ONNX) is exercised only via the live
  end-to-end smoke test, not in unit tests.

## Run

```sh
make run-qdrant            # start Qdrant
make run                   # build UI + run server (UI embedded) → http://localhost:4334
make build                 # one self-contained binary → bin/markdex-<os>-<arch>
```

For UI hot reload, run the backend and `cd web && npm run dev` separately (Vite on
`:5173` proxies `/api` to `:4334`).
