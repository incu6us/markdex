# Roadmap — from ingester to robust knowledge base

markdex today is a strong **ingestion + storage** pipeline. To be a robust knowledge
base for AI agents it still needs a **retrieval layer it controls**, a stronger
embedder, broader source coverage, and basic production hardening.

This file tracks the gaps. Check an item off (`- [x]`) when it ships, with a one-line
note + commit/PR reference.

Legend: `[ ]` todo · `[~]` in progress · `[x]` done.

## Tier 1 — Retrieval layer (highest leverage)

The half that determines answer quality. Today retrieval is delegated to the external
`mcp-server-qdrant` (`qdrant-find`), which does plain dense kNN with the ingest model.
markdex has no search endpoint of its own.

- [ ] **`/api/search` endpoint** — a retrieval API markdex owns (query → ranked chunks
      with scores + metadata), so we control ranking instead of delegating it.
- [ ] **Hybrid search** — dense + sparse (BM25/SPLADE) via Qdrant named/sparse vectors;
      catches exact-term matches (API names, identifiers) that pure dense misses.
- [ ] **Cross-encoder reranking** — rerank top-k candidates; usually the single biggest
      precision win.
- [ ] **Query-time metadata filters** — filter by `source_id` / `title` / `heading_path`
      (Qdrant supports it; just needs to be exposed).
- [ ] **Parent-document retrieval** — match on small chunks, return the larger enclosing
      section to the LLM.
- [ ] **Keep MCP compatibility** — preserve the `qdrant-find` payload contract while adding
      the richer search path.

## Tier 2 — Embedding & chunk quality

- [ ] **Stronger / longer-context embedder** — `bge-small` is 384-dim / 512-token. Evaluate
      `bge-m3` (dense+sparse+ColBERT, 8k ctx) or Voyage (Anthropic-recommended embeddings).
- [ ] **Contextual retrieval** — prepend a one-line doc/section context to each chunk before
      embedding (uses existing `title`/`heading_path`); ~35–50% fewer retrieval failures.
- [ ] **Token-accurate chunk sizing** — replace the rune approximation with real tokenizer
      counts so chunks fit the model window exactly.
- [ ] **Near-duplicate chunk dedup** — drop near-identical windows to cut noise.

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

- [ ] **Retrieval eval harness** — a golden query set + recall@k / MRR so retrieval quality
      is measurable. Without numbers we can't call it "robust."

---

## Already shipped (foundation)

- [x] Markdown ingestion: structural H1 split → recursive H2/H3 → sliding-window-with-overlap;
      code-fence aware; every chunk fits the model window.
- [x] Idempotent re-ingest: delete-by-`source_id` then upsert (no orphaned chunks on change).
- [x] MCP-compatible payload (`document` + `metadata`, named vector `fast-bge-small-en-v1.5`).
- [x] Sources: file upload + single raw GitHub `.md` URL (`/blob/`→raw).
- [x] HTTP API: preview / collections (list + create) / async ingest / job status + SSE.
- [x] Collection dim/model pre-check (`409` on mismatch).
- [x] React UI served from the same origin, embedded into the binary via `//go:embed`.
- [x] Self-contained binary (`make build`) + Docker Compose (app + Qdrant).
- [x] Consistent chunking between preview and ingest (shared default `max_chars`/`overlap`).
- [x] TDD throughout: unit tests against fakes/`httptest`; verified end-to-end against live
      ONNX + Qdrant.
