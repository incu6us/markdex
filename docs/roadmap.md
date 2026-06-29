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
- [x] **Agent write-back (memory)** — markdex is now writable: `POST /api/memories` +
      `remember`/`forget` MCP tools, with **semantic supersede** (a near-identical existing memory is
      replaced in place — lexical Jaccard pre-gate, then cross-encoder rerank score above
      `-supersede-threshold` — else appended) and a **configurable target collection** (a dedicated
      `*-memory` collection or an existing doc collection). Reuses the retrieval spine (`Replace` =
      delete-by-source + upsert; dup probe via `/api/search` filtered to `type="memory"`, which is
      also the **clobber guard** — a memory can never replace a curated doc). Memories carry
      `type`/`author`/`created_at`/`updated_at`/`version`/`namespace`/`tags`. Strict-TDD/DDD across
      domain → application (`MemoryService`) → httpapi → markdexclient → MCP. Verified end-to-end on
      the live stack: append → search → supersede (paraphrase, point count flat, `version` 2) →
      append (unrelated, +1) → forget (gone from search); plus the clobber guard (memory into the
      115-pt `go-style-guide` doc collection: 115→116→115, no doc deleted, point tagged
      `type=memory`). Ships **open**; all writes route through a no-op `requireAuth` seam so auth is a
      one-place follow-up (Tier 4). Plan in [memory_plan.md](memory_plan.md).
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
      already nails.
      **Reranker breadcrumb-awareness (done):** search now reranks the same contextual text
      (`domain.ContextualText` rebuilt from the candidate's `heading_path`) instead of the raw
      document, while the returned hit keeps raw content. *Measured on the 22-query set:* MRR
      **0.898 → 0.909**, Hit@1 **82% → 86%** (e.g. "when to panic" 2→1), with Hit@3 91% (one
      query, "acceptance test", slid 2→4 as its breadcrumb adds a diluting middle segment).
      Tellingly, the breadcrumb-embedded and raw-embedded collections now score **identically** —
      confirming the reranker, not the retrieval embedding, sets the final order.
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
- [x] **Repo / folder ingestion** — `github_repo` source lists every `.md` in a GitHub repo (or
      a `/tree/<branch>/<subpath>`) via the git trees API (`github.RepoLister`) and ingests them
      all in one async job; per-file fetch failures are skipped + logged. Optional `GITHUB_TOKEN`
      raises the 60/hr unauthenticated limit and enables private repos. Verified end-to-end
      ingesting `incu6us/markdex` (README + docs → 25 chunks); httptest-mocked listing + handler
      tests. UI: a **GitHub repo** source tab in Ingest.
      **Local folder** ingestion too: the `upload_dir` source carries `[{name, content}]` and the
      UI's **Local folder** tab reads every `.md` in a picked folder client-side
      (`webkitdirectory`) — no volume mount needed, empty files skipped. Verified e2e.
      **Private repos** are supported: with `GITHUB_TOKEN` set, the lister authenticates the API
      and the fetcher pulls each file through the authenticated contents API
      (`Accept: application/vnd.github.raw`) rather than `raw.githubusercontent.com`. Verified
      end-to-end against a **real private repo** (created → ingested → searched → deleted).
- [ ] **Scheduled / incremental re-sync** — refresh sources on a schedule, not just manually.
- [x] **Collection reconciliation** — re-ingesting a `github_repo` with `prune: true` deletes
      chunks for files that no longer exist in the repo (idempotent re-ingest only replaced docs
      it *re*-ingested, leaving vanished ones as orphans). Scoped to the repo's raw-URL prefix
      (`domain.SourcesToPrune`) so other sources in the collection are never touched; backed by
      `Repository.ListSources` + `DeleteSources`. UI: a "remove deleted files" checkbox on the
      GitHub repo source. Verified e2e (whole repo 170 pts → re-ingest `docs/` subpath with prune
      → 30 pts, README's chunks gone from search).

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
