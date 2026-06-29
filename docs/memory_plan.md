# Memory plan — agent write-back (`remember` / `forget`)

Turn markdex from a **read-only knowledge base** into one agents can also **write to**:
a runtime "memory" an agent (or user) accumulates — code styles, rules, business cases,
facts learned mid-task — and that future agents retrieve through the existing hybrid
search. This is the missing inverse of the retrieval layer: today humans/repos author
docs and agents read; this adds an agent-facing write path.

This document is the single source of truth for the feature's scope, decisions, DDD
layering, and TDD build order. It follows the conventions of [sidecar_plan.md](sidecar_plan.md).

## What "memory" is (and is not)

- A **memory** is one short, atomic, current-state fact written at runtime
  (`"Acme is on legacy billing."`), with provenance (author, timestamps) and optional
  tags/namespace. It is **not** a document — no H1 split, no batching, no async job.
- Memory is **current-state, not a log**: superseding a fact replaces it in place. A light
  audit trail (`version`, `supersedes`, `updated_at`) is kept in metadata; full history is
  out of scope (would need append-with-tombstone — deferred).
- Memory is **lower-trust** than curated docs. The `metadata.type` field separates the two
  at retrieval time and — critically — is the safety boundary that lets memories live safely
  inside a doc collection (see [Decision: clobber guard](#decisions-locked)).

## Decisions (locked)

| Topic | Decision |
|---|---|
| **Placement** | **Configurable target collection.** `remember` takes a `collection`; a memory may go to a dedicated `*-memory` collection **or** into an existing doc collection. Either way it is tagged `metadata.type="memory"`. |
| **Write / identity semantics** | **Semantic supersede.** Before writing, search the target for near-identical existing **memories**; if the best match clears a threshold, replace it in place (reuse its `source_id`); else append under a fresh `memory:<uuid>`. |
| **Clobber guard** | The supersede candidate search **must** filter `type="memory"`. A curated doc can never be a supersede target, so `remember` into a mixed collection can never delete a document. |
| **Similarity signal** | **Lexical pre-gate then semantic gate.** Word-shingle Jaccard (reuse `domain.DedupeChunks` logic) catches restatements for free; cross-encoder rerank score with a tunable `supersedeThreshold` catches paraphrases. **Calibrated to 0.97** (see [Threshold calibration](#threshold-calibration)) — the lowest cutoff with zero wrong supersedes on a labeled pair set. Errs toward *append* (a missed dedup is harmless; a wrong supersede loses info). |
| **Auth** | **Write-back now, auth later.** Endpoints ship open but route through a single pass-through `requireAuth` seam so auth becomes a one-place diff. Tracked as the immediate follow-up before any shared/team use. |
| **Mutability** | `forget` (delete a memory by id) ships alongside `remember`, backed by the existing `Repository.DeleteSources`. |
| **Sync vs async** | **Synchronous.** One embed call (~0.15 s) + one search + one upsert — no `JobManager` (unlike ingest's batched async path). |
| **Testing / style** | Strict **TDD** (failing unit test first), Clean Architecture / DDD layering, Google Go style, small additive interfaces, explicit error wrapping. |

## Architecture (DDD layering)

The feature reuses the existing ports and the retrieval spine; new code is thin and lives
at each layer's natural seam.

```
remember(text) ─► MemoryService ─► SearchService (dup probe, type=memory filter)
                       │                 └─► Embedder + VectorRepository.Search (reused)
                       ├─► Embedder.Embed(text, Document)            (reused)
                       └─► VectorRepository.Replace(memSourceID, [1])(reused: delete-by-source + upsert)
forget(id)    ─► MemoryService ─► VectorRepository.DeleteSources     (reused)
```

- **`domain`** — the only model change: `ChunkParams.Metadata map[string]string`, a free-form
  bag merged into the stored payload. The clobber-guard and supersede *policy* are expressed in
  the application layer; the domain stays a pure value/port layer. (`Chunk` keeps its five typed
  fields; `Metadata` is additive and optional, so every existing caller is unaffected.)
- **`application`** — new `MemoryService` orchestrating `probe → decide → embed → replace`
  (`Remember`) and `delete` (`Forget`). Depends only on domain ports + the existing
  `SearchService`. Holds the `supersedeThreshold` and the lexical/semantic gate.
- **`infrastructure`**
  - `qdrant` — **no new method for the happy path**: a memory write is `Replace` with a unique
    `source_id`; an in-place supersede is `Replace` with the matched `source_id`. The metadata bag
    is merged into the existing payload builder. `DeleteSources` already covers `forget`.
  - `httpapi` — `POST /api/memories`, `DELETE /api/memories/{id}`, behind a no-op `requireAuth`.
  - `markdexclient` — `Remember` / `Forget` methods over the new endpoints.
- **`cmd/mcp`** — `remember` / `forget` tools (`ReadOnlyHint:false`) on the `markdexService`
  interface. The `remember` tool is what flips markdex from readable to writable for agents.

### Why no new repository method

`VectorRepository.Replace(ctx, sourceID, chunks)` is **delete-by-`source_id` then upsert**.
With a per-memory `source_id` it is a pure **append**; called again with the same `source_id`
it is an in-place **update**. `Chunk.ID() = UUIDv5(source_id#index)` makes a single-chunk
memory's point ID stable and collision-free. The storage verb for memory already exists.

## Data model — the memory point

A memory is a single-chunk point. `source_id = "memory:<uuid>"` (new) or the matched
memory's `source_id` (supersede). Payload `document` is the raw fact; the new `metadata` keys:

```jsonc
{
  "type":       "memory",                 // mandatory: search hygiene + clobber guard
  "author":     "agent:claude-code",      // or user:<id>
  "created_at": "2026-06-29T12:00:00Z",   // time.Now().UTC() (Go runtime — no restriction)
  "updated_at": "2026-06-29T12:00:00Z",   // bumped on supersede
  "version":    1,                         // incremented on supersede
  "supersedes": "",                        // prior source_id replaced (audit), empty on first write
  "namespace":  "team:payments",          // optional: scopes supersede + retrieval
  "tags":       "billing,acme"            // optional: filterable
}
```

The existing doc keys (`path`, `source_id`, `title`, `heading_path`, `chunk_index`) are still
written; `title`/`heading_path` may be empty for a memory (so `Section`/`Headings` ignore it).

## `MemoryService.Remember` — algorithm

```
Remember(ctx, collection, text, meta):
  ensureCollection(ctx, collection)                       // create w/ BGE-M3 schema if absent
  // 1. dup probe — MEMORIES ONLY (clobber guard)
  cands := search.Search(ctx, collection, text, topK=3, Filter{type:"memory"}, expand=false)
  // 2. decide
  if len(cands) > 0 && supersedes(text, cands[0]):        // lexical Jaccard OR rerank >= threshold
      sourceID  := cands[0].Metadata["source_id"]
      meta.supersedes = sourceID
      meta.version    = prevVersion(cands[0]) + 1
  else:
      sourceID  := "memory:" + uuid()
      meta.version = 1
  meta.type, meta.updated_at = "memory", now()
  // 3. embed + store (reused)
  v     := embedder.Embed(ctx, [text], Document)[0]
  chunk := NewChunk{SourceID: sourceID, Index: 0, Content: text, Metadata: meta}
  repo(collection).Replace(ctx, sourceID, [{chunk, v}])
  return sourceID
```

`supersedes(new, candidate)` = word-shingle Jaccard ≥ `dedupThreshold` (cheap, exact restatements)
**OR** candidate rerank score ≥ `supersedeThreshold` (paraphrases). Both are config knobs and the
defaults err toward *append*. The semantic threshold was calibrated empirically (below) — note
`/api/eval` measures *retrieval* quality (MRR/Hit@k), not paraphrase-equivalence, so it is **not**
the right instrument; calibration needs labeled same/different memory **pairs**.

`Forget(ctx, collection, id)` → `repo(collection).DeleteSources(ctx, [id])`.

## Threshold calibration

The supersede gate thresholds the cross-encoder rerank score of the top `type="memory"` candidate.
That score is uncalibrated, so the cutoff was measured against a labeled set of **16 memory pairs**
(8 *same fact, paraphrased* → should supersede; 8 *related but distinct* → should append), each run
through the real probe on the live stack: store A, probe with B, record `results[0].score`.

The asymmetry that drives the choice: a **wrong supersede destroys information**, a **missed dedup
is harmless** (a duplicate, cleanable later). So the target is the *lowest* cutoff with **zero wrong
supersedes**, maximizing true-paraphrase coverage without ever overwriting a distinct fact.

| Cutoff | Accuracy | Missed dedup (harmless) | **Wrong supersede (bad)** |
|---|---|---|---|
| 0.90–0.965 | 14/16 | 1 | **1** |
| **0.97–0.985** | **15/16** | 1 | **0** |
| 0.99 | 14/16 | 2 | 0 |
| 0.995 | 12/16 | 4 | 0 |

The single wrong supersede below 0.97 is the instructive case: *"Acme is on the legacy billing plan"*
vs *"Acme is on the premium support tier"* scored **0.9662** — same entity, different attribute, the
exact confusion to avoid. **0.97** (lower edge of the safe band) is the chosen default — verified
live: the premium-tier pair now appends, while a clean paraphrase still supersedes. The lone
remaining miss is a SAME pair at **0.7655** (*"two approvals"* ↔ *"two reviewers sign off"*) that
appends — acceptable, and the lexical pre-gate or a future contextual probe could recover it.

Re-run per corpus when the domain shifts; the harness is a store-A / probe-B / read-score loop over
a labeled pair set (kept in this doc, not committed as code — it needs the live reranker).

## HTTP API

| Endpoint | Purpose |
|---|---|
| `POST /api/memories` | `{collection, text, author?, namespace?, tags?}` → embed + supersede-or-append → `201 {source_id, superseded}`. Synchronous. |
| `DELETE /api/memories/{id}` | Delete one memory by its `source_id` (`forget`). `204`. |

Both register through a `requireAuth` middleware that is a **pass-through no-op today**; a visible
`// TODO(auth): gate write routes` marks the seam. When auth lands it is a single token check there.

## MCP tools

| Tool | Annotations | Purpose |
|---|---|---|
| `remember` | `ReadOnlyHint:false` | Store a fact in a collection; supersedes a near-identical existing memory or appends. Returns the memory id + whether it superseded. |
| `forget` | `ReadOnlyHint:false`, `DestructiveHint:true` | Delete a memory by id. |

Added to the `markdexService` interface (`cmd/mcp/tools.go`) and the `markdexclient` adapter,
mirroring the existing read-only tools.

## Phasing (strict TDD; each lands green before the next)

1. **Domain — metadata bag.** Failing test: a `Chunk` built with `Metadata{type:"memory", author:…}`
   surfaces those keys under `metadata.*` in the qdrant payload. Then add `ChunkParams.Metadata`
   and merge it in the payload builder. Additive — all existing tests stay green.
2. **Application — `MemoryService` (fakes).** Failing tests first, for each branch:
   *append* (no candidate), *supersede-hit* (reuses candidate `source_id`, bumps `version`),
   *never-clobbers-a-doc* (a doc candidate is filtered out → append, not replace),
   *threshold boundary* (just-below → append, at/above → supersede),
   *lexical pre-gate* (exact restatement supersedes without relying on rerank).
   Then implement `Remember`/`Forget` against `domain.Embedder`, `domain.VectorRepository`,
   and `application.SearchService` (or a small search port) + injected thresholds.
3. **HTTP — endpoints (`httptest`).** Failing handler tests: `POST /api/memories` happy path
   (`201`, returns id), validation (`400` on empty text/collection), `DELETE` (`204`); assert the
   `requireAuth` seam is wired (no-op). Then implement handlers + wiring in `server.go`.
4. **markdexclient + MCP — tools (fake svc).** Failing client tests against an `httptest` mock of
   the two endpoints; failing tool tests (mirroring `tools_test.go`) for `remember`/`forget`.
   Then implement the client methods and register the tools.
5. **E2E — live stack.** remember a fact → search finds it → remember a paraphrase → assert
   **supersede** (point count flat, text updated, `version` 2) → remember an unrelated fact →
   assert **append** (count +1) → `forget` → assert gone from search. Confirm a memory written
   **into a doc collection** never deletes a doc (clobber guard).
6. **Docs.** README (new endpoints + MCP tools + the memory concept), roadmap check-off, this file.

## Testing strategy

- TDD: failing unit test first, then the implementation; table-driven `t.Run` + `t.Parallel()`.
- Hand-written fakes for `Embedder`, `VectorRepository`, and the search dependency; the supersede
  decision is unit-tested with a deterministic rerank/Jaccard stub (no live models).
- `httptest` request/recorder for the handlers and the `markdexclient` adapter.
- Native models (BGE-M3 + reranker) exercised only via the live E2E in Phase 5.

## Status

- [x] Phase 1 — domain metadata bag. `ChunkParams.Metadata map[string]string` (defensively copied,
      nil-safe); merged into the qdrant payload with reserved keys (`path`/`source_id`/`title`/
      `heading_path`/`chunk_index`) set last so the bag can't spoof them. TDD: domain accessor tests
      + qdrant payload-merge test (incl. spoof guard).
- [x] Phase 2 — `MemoryService` (append / supersede / clobber-guard / thresholds), TDD with fakes.
      New ports `MemorySearcher` + `MemoryStore`; lexical pre-gate via exported
      `domain.ShingleSimilarity`; injectable clock + id generator. Tests: append, probe-memories-only
      (clobber guard), semantic supersede (+version bump, created_at preserved), threshold boundary,
      lexical pre-gate, validation, forget.
- [x] Phase 3 — `POST /api/memories` + `DELETE /api/memories/{id}` + `requireAuth` seam, TDD httptest.
      Reuses the ingest dim/model pre-check (`409` on mismatch). Wired in `main.go` (`memoryStore` +
      `memorizer` adapters; the existing `chunkSearcher` doubles as the supersede probe).
- [x] Phase 4 — `markdexclient` `Remember`/`Forget` + MCP `remember`/`forget` tools, TDD. Client `do`
      generalized to any 2xx (+ no-body 204). Tools annotated as writes (`forget` destructive).
- [x] Phase 5 — E2E on the live 3-service stack (Docker). append → search finds it → supersede
      (paraphrase: same `source_id`, point count flat, `version` 2) → append (unrelated, +1) → forget
      (gone from search). Clobber guard: a memory written into the 115-pt `go-style-guide` doc
      collection went 115→116→115 with **no document deleted**, the point tagged `type=memory`.
- [x] Phase 6 — docs (README API table + "Agent memory" section + MCP tools table; roadmap check-off;
      this file).
- [x] Threshold calibration — `supersedeThreshold` set to **0.97** from a 16-pair labeled set;
      zero wrong supersedes, verified live (see [Threshold calibration](#threshold-calibration)).

## Post-plan enhancements (shipped)

- [x] **Per-request threshold override** — optional `supersede_threshold` (0,1] on `POST /api/memories`
      and the `remember` MCP tool, threaded application → httpapi → markdexclient → MCP (TDD each;
      validated at the HTTP/MCP boundaries). Lets the UI/agent merge more/less aggressively for one
      write without restarting; omit to use the calibrated default. Also exposed as the
      `-supersede-threshold` server flag. Verified live (default appends a 0.9662 pair; override 0.5
      supersedes it).
- [x] **Memory list endpoint** — `GET /api/collections/{name}/memories` (Qdrant scroll filtered
      `type="memory"`, newest-first) → `qdrant.ListMemories` + `httpapi.MemoryLister`. TDD.
- [x] **Memory UI tab** — a **Memory** tab: remember form (text/author/namespace/tags + an *advanced*
      merge-threshold slider), the collection's memories list, and per-row *forget*. Vite build clean,
      embedded in the binary, verified live (screenshot).

## Deferred / follow-ups

- **Auth** — the immediate follow-up; gate the write routes at the `requireAuth` seam
  (shared secret → per-team keys → doc-level ACLs). Tier 4 in [roadmap.md](roadmap.md). **This is the
  only remaining must-do before shared/multi-tenant use** — everything else below is optional.
- **Full history** — append-with-tombstone instead of in-place supersede, if an audit log of
  superseded facts is ever needed.
- **Memory decay (TTL)** — expire stale memories; needs `created_at`-based reconciliation.
- **Promote memory → curated doc** — a path to graduate a high-value memory into an ingested doc.
- **Supersede scope** — currently global within `type="memory"`; consider scoping to `namespace`
  (would also recover the one calibration miss where a true paraphrase scored below 0.97).
- **Recover sub-threshold paraphrases** — e.g. probe the contextual text, or a small ensemble, for
  the ~0.77-scoring "same fact" cases the single cutoff misses.
