# Eval golden sets

Each `*.json` is a golden query set for the retrieval eval harness (`cmd/eval`).

## go-style-guide

- `go-style-guide.json` — 16 queries mapped to the doc's real section slugs.
- `go-style-guide.md` — a **pinned copy** of the source document, vendored so the eval is
  reproducible from an empty Qdrant (`make eval-seed` ingests it). Pinning also keeps the
  baseline numbers stable even if the upstream doc changes.

Source: Google Style Guide — `go/best-practices.md`
(<https://github.com/google/styleguide/blob/gh-pages/go/best-practices.md>),
licensed under **Apache License 2.0**. Vendored verbatim for evaluation purposes.
