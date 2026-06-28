# Sidecar plan — local hybrid retrieval + reranking (BGE-M3)

Move embeddings out of the Go process into a Python **embedder sidecar** that hosts
**BGE-M3** (dense + sparse) and **bge-reranker-v2-m3** (cross-encoder reranking). This
delivers, fully local and free:

- Tier 1 — `/api/search` endpoint, hybrid dense+sparse search, cross-encoder reranking,
  query-time filters.
- Tier 2 — a stronger, longer-context embedder.

A pleasant side effect: the Go binary drops `fastembed-go` + ONNX entirely and becomes
pure Go (the app `Dockerfile` no longer installs the ONNX Runtime).

## Architecture

```
┌─────────────┐    HTTP     ┌──────────────────────┐
│  markdex    │ ──embed───► │ embedder (Python)    │  BGE-M3: dense + sparse
│  (Go)       │ ──rerank──► │  FlagEmbedding       │  bge-reranker-v2-m3
└──────┬──────┘             └──────────────────────┘
       │ upsert / query (dense + sparse)
       ▼
   ┌────────┐
   │ Qdrant │  hybrid: dense + sparse named vectors, RRF fusion
   └────────┘
```

## 1. Sidecar service (`services/embedder/`, Python + FastAPI)

```
POST /embed    {texts:[...], kind:"document"|"query"}
            →  {dense:[[…1024…]], sparse:[{indices:[…], values:[…]}]}
POST /rerank   {query:"…", documents:[…], top_k:8}
            →  {results:[{index, score}, …]}
GET  /healthz  → 200 when models are loaded (503 otherwise)
GET  /info     → {dense_dim, dense_name, sparse_name, embed_model, rerank_model}
```

- Models load once at startup (warm). `/info` lets the Go app discover dim/vector-names
  instead of hardcoding.
- `kind` is accepted for API stability; BGE-M3 is symmetric so it needs no query/doc prefix.
- ColBERT/multivector output is **deferred** (heavier storage, marginal gain over
  dense+sparse+rerank).

## 2. Go domain ports

```go
type SparseEmbedding struct { indices []uint32; values []float32 }
type Vectors struct { Dense Embedding; Sparse SparseEmbedding }

type EmbedKind int            // Document | Query
type Embedder interface {
    Embed(ctx, texts []string, kind EmbedKind) ([]Vectors, error)
    Dimension() int
}

type Ranked struct { Index int; Score float32 }
type Reranker interface {
    Rerank(ctx, query string, docs []string, topK int) ([]Ranked, error)
}

type EmbeddedChunk struct { Chunk Chunk; Vectors Vectors }   // was {Chunk, Embedding}

type VectorRepository interface {
    Prepare(ctx, schema CollectionSchema) error              // dense dim + sparse
    Replace(ctx, sourceID string, chunks []EmbeddedChunk) error
    Search(ctx, collection string, q Vectors, topN int, f Filter) ([]SearchHit, error)
}
type SearchHit struct { ID string; Score float32; Document string; Metadata map[string]string }
```

## 3. Application layer

`IngestService` embeds with `Document` and stores dense+sparse. New `SearchService`:

```
Search(ctx, collection, query, topK, filter):
    qv         := embedder.Embed(ctx, [query], Query)[0]
    candidates := repo.Search(ctx, collection, qv, poolSize=50, filter)   // hybrid RRF
    ranked     := reranker.Rerank(ctx, query, docs(candidates), topK=8)
    return reorder(candidates, ranked)
```

## 4. Qdrant hybrid schema

```jsonc
// create
{ "vectors": {"bge-m3-dense": {"size":1024,"distance":"Cosine"}},
  "sparse_vectors": {"bge-m3-sparse": {}} }
// query: prefetch dense + sparse, fuse with RRF, then rerank top-50 → top-8
{ "prefetch":[ {"query":[…dense…],"using":"bge-m3-dense","limit":50},
               {"query":{"indices":[…],"values":[…]},"using":"bge-m3-sparse","limit":50} ],
  "query":{"fusion":"rrf"}, "limit":50, "with_payload":true, "filter":{…} }
```

Qdrant 1.18.2 supports prefetch + fusion natively.

## 5. Infra changes

- **New** `internal/infrastructure/embedderclient` — HTTP client to the sidecar; implements
  `Embedder` + `Reranker`.
- **`qdrant`** — `Prepare`/`Replace`/`Search` updated for dense+sparse.
- **`httpapi`** — `POST /api/search`.
- **Removed** the `fastembed` package and ONNX → pure-Go binary; the app `Dockerfile` drops
  the onnxruntime install (can go distroless).
- **compose** — add `embedder` service + HF cache volume; `app` gets `EMBEDDER_URL` and
  `depends_on: [qdrant, embedder]`.

## 6. Phasing (TDD; each lands green before the next)

1. **Sidecar** — Python service + Dockerfile; smoke `/embed` `/rerank` `/healthz` `/info`. ← current
2. **Domain** — `SparseEmbedding`, `Vectors`, `EmbedKind`, `Reranker`, updated
   `EmbeddedChunk`/`VectorRepository` (unit tests).
3. **embedderclient** — TDD against an httptest sidecar mock.
4. **qdrant** — Prepare/Replace/Search hybrid, TDD httptest (assert prefetch+fusion body).
5. **IngestService** — adjust for dense+sparse (update existing tests).
6. **SearchService** — TDD with fakes.
7. **`/api/search`** — TDD httptest.
8. **Wiring** — main, drop fastembed/onnx, compose, Dockerfiles.
9. **E2E** — live stack, re-ingest, validate hybrid+rerank vs current.
10. **Docs** — README + roadmap check-offs.

## Decisions

- Drop the local `fastembed-go` path entirely (one embedding path = simpler).
- `pool_size=50 → top_k=8`, both per-request overridable.
- Pin `BAAI/bge-m3` + `BAAI/bge-reranker-v2-m3`; CPU by default, GPU via env later.
- MCP: the dense vector name changes, so stock `qdrant-find` no longer matches — markdex
  owns retrieval via `/api/search`; an MCP tool wrapper is a later roadmap item.

## Status

- [x] Phase 1 — sidecar service. Verified in-container: `/healthz`, `/info` (dense_dim
      1024), `/embed` (1024-d dense + sparse terms), `/rerank` (correct relevance order).
      Reranking uses the transformers `AutoModelForSequenceClassification` pattern (avoids a
      FlagReranker/transformers slow-tokenizer incompatibility).
- [x] Phase 2 — domain primitives (additive, TDD): `SparseEmbedding` (+validation),
      `Vectors`, `EmbedKind` (Document/Query), `Reranker` port + `Ranked`. Kept additive so
      the module stays green; the breaking `EmbeddedChunk`/`VectorRepository`/`Embedder`
      signature swap rides with the qdrant + ingest rewrite (Phases 4–5).
- [x] Phase 3 — `embedderclient` (TDD): HTTP client to the sidecar. `Embed` (texts+kind →
      `[]Vectors`), `Rerank` (implements `domain.Reranker`), `Info` (dim/vector names).
      Tested against an `httptest` mock mirroring the real sidecar JSON.
- [ ] Phase 4 — qdrant: `Prepare`/`Replace`/`Search` for dense+sparse (hybrid, RRF)
- [ ] Phases 5–10
