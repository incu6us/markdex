# Roadmap — from ingester to robust knowledge base

markdex today is a strong **ingestion + storage** pipeline. To be a robust knowledge
base for AI agents it still needs a **retrieval layer it controls**, a stronger
embedder, broader source coverage, and basic production hardening.

This file tracks the gaps. Check an item off (`- [x]`) when it ships, with a one-line
note + commit/PR reference.

Legend: `[ ]` todo · `[~]` in progress · `[x]` done.

## Tier 1 — Retrieval layer (highest leverage)

The half that determines answer quality. markdex owns retrieval end-to-end via `/api/search`:
hybrid (dense + sparse) candidate retrieval fused with RRF, then cross-encoder reranking.

- [x] **`/api/search` endpoint** — markdex owns retrieval: query → embed → hybrid Qdrant
      search → rerank → ranked hits with scores + metadata. (sidecar Phases 4–8)
- [x] **Hybrid search** — dense + sparse (BGE-M3 lexical weights) via Qdrant named/sparse
      vectors, fused with RRF. Verified live against Qdrant 1.18.2.
- [x] **Cross-encoder reranking** — a cross-encoder in the sidecar reorders the candidate pool
      (default `cross-encoder/ms-marco-MiniLM-L-6-v2`, pool 24 → top-k; swappable via
      `RERANK_MODEL`). Verified live.
- [x] **Query-time metadata filters** — `filter` map on `/api/search` → Qdrant `must`
      conditions on `metadata.*`.
- [x] **Parent-document retrieval** — `/api/search` `expand` reassembles the full heading
      section (all chunks sharing `source_id` + `heading_path`, de-overlapped) and returns it
      in place of the matched chunk. Verified live (2 KB chunk → 8 KB section). Search-UI toggle.
- [x] **Expose retrieval as MCP tools** — `cmd/mcp` is an MCP (stdio) server on the official
      [`go-sdk`](https://github.com/modelcontextprotocol/go-sdk), exposing three read-only tools
      (`search`, `list_collections`, `list_headings`) over the REST API via the
      `markdexclient` adapter; register with `claude mcp add markdex -- go run ./cmd/mcp`.
      Typed I/O + structured output + tool annotations; protocol-version negotiation handled by
      the SDK. Verified end-to-end (initialize → tools/list → call).
- [x] **Collections management UI** — dedicated **Collections** tab listing every collection
      (name, points, dimension) with create + delete; delete asks for a single confirmation and
      is backed by `DELETE /api/collections/{name}` → Qdrant `Repository.Delete`. Selected
      collection + active tab persist across tabs and reloads.
- [x] **Search UI** — Ingest/Search nav in the React app; collection picker + query + `top_k`
      → ranked results (title, heading_path, rerank score, snippet) over `/api/search`.

## Tier 2 — Embedding & chunk quality

- [x] **Stronger / longer-context embedder** — swapped `bge-small` (384-dim/512-token) for
      **BGE-M3** (1024-dim dense + sparse, 8k context) in the Python embedder sidecar. The Go
      binary is now pure-Go (no ONNX). (sidecar Phases 1–8)
- [x] **Contextual retrieval (heading-path breadcrumb)** — each chunk is embedded as
      `humanized(heading_path) + "\n\n" + content` (`domain.Chunk.ContextualText`), so the dense
      **and** sparse vectors encode where the chunk sits in the document. The stored payload stays
      the raw content, so search results and `expand` (parent-document retrieval) are unchanged.
      Authoring implication: headings are now retrieval metadata — descriptive, well-nested
      `#/##/###` headings produce better breadcrumbs.
      *Measured:* **no delta on the `go-style-guide` golden set (MRR 0.898, Hit@1 82%, both
      runs)** — that 22-query set already has **100% Hit@10**, so recall is saturated and the
      final order is set by the reranker (which scores the raw document). The benefit is a
      recall/disambiguation lever; it shows up on larger/noisier corpora, not one the system
      already nails. Next lever to convert this into a ranking gain: make the **reranker**
      breadcrumb-aware (reconstruct from stored `heading_path` metadata at rerank time).
- [x] **Token-accurate chunk sizing** — the embedder exposes `POST /tokenize` (real BGE-M3
      tokenizer); ingest counts each chunk's *embedded* (contextual) text and re-splits any chunk
      over `embedderMaxTokens` (8192, = the model window) into rune windows that fit, verified
      against real counts (`IngestService.enforceTokenBudget`). *Measured:* on `go-style-guide`,
      **0 re-splits** — chunks are ~500–700 tokens, far under 8192, so it's a correctness
      guarantee, not a live fix; activation is unit-tested with a deterministic counter.
- [x] **Near-duplicate chunk dedup** — `domain.DedupeChunks` drops chunks whose word-shingle
      Jaccard ≥ 0.9 against an earlier kept chunk, applied before embedding. *Measured:* on
      `go-style-guide`, **0 dropped** (no near-dups; sliding-window overlap is only ~10% → well
      below the threshold, so legitimate adjacent windows are kept). Unit-tested.
      Both run only at ingest; refined chunks are re-indexed so chunk IDs stay unique/ordered.

## Tier 3 — Source coverage & freshness

- [ ] **More source types** — PDF, HTML, docx (markdown only today).
- [ ] **Repo / folder ingestion** — pull every `.md` from a repo/path (currently single raw
      `.md` URL or upload).
- [ ] **Scheduled / incremental re-sync** — refresh sources on a schedule, not just manually.
- [ ] **Collection reconciliation** — remove points for source docs that no longer exist
      (delete-by-source only handles re-ingested docs; vanished ones leave orphans).

## Tier 4 — Production hardening

- [ ] **Authentication / authorization** — the API is currently open (anyone reachable can
      create/overwrite/wipe collections). Add auth; consider per-collection / doc-level ACLs
      for multi-tenant agents.
- [ ] **Observability** — OpenTelemetry traces + metrics (latency, throughput, error rate)
      at the HTTP/job boundary; structured logs already in place.
- [ ] **Durable job store** — ingestion jobs are in-memory and lost on restart; persist them.
- [ ] **Concurrency / scale** — single-worker serial ingestion today (embedder is not
      concurrency-safe); add safe parallelism / batching across documents.
- [ ] **Rate limiting** on ingest/search.

## Tier 5 — Evaluation

- [x] **Retrieval eval harness** — `cmd/eval` posts a golden query set to `/api/search` and
      reports **MRR / Hit@1 / Hit@3 / Hit@k**; pure scoring logic is unit-tested. Run with
      `make eval` (or `make eval-seed` to ingest the pinned fixture first). Baseline on
      `go-style-guide` (16 queries): MRR 0.91, Hit@1 0.88, Hit@10 1.0. Use it to detect
      regressions and compare configs (reranker model, pool size).
- [x] **Eval as a backend endpoint** — scoring moved into the application layer (one tested
      source of truth) behind `POST /api/eval`; `cmd/eval` and the UI **Eval** tab are thin
      clients. The UI tab runs an editable golden set and shows MRR / Hit@k + per-query ranks
      for interactive config A/B testing.

---

## Already shipped (foundation)

- [x] Markdown ingestion: structural H1 split → recursive H2/H3 → sliding-window-with-overlap;
      code-fence aware; every chunk fits the model window.
- [x] Idempotent re-ingest: delete-by-`source_id` then upsert (no orphaned chunks on change).
- [x] Payload `document` + `metadata` (`path`, `source_id`, `title`, `heading_path`,
      `chunk_index`) under named vectors `bge-m3-dense` + `bge-m3-sparse`.
- [x] Sources: file upload + single raw GitHub `.md` URL (`/blob/`→raw).
- [x] HTTP API: preview / collections (list + create) / async ingest / search / job status + SSE.
- [x] Collection dim/model pre-check (`409` on mismatch).
- [x] React UI served from the same origin, embedded into the Go binary via `//go:embed`.
- [x] Docker Compose: app (pure-Go) + embedder sidecar (BGE-M3) + Qdrant.
- [x] Consistent chunking between preview and ingest (shared default `max_chars`/`overlap`).
- [x] TDD throughout: unit tests against fakes/`httptest`; verified end-to-end against the
      live 3-service stack (app + BGE-M3 embedder + Qdrant).
