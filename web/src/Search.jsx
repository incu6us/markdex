import { useState } from 'react'
import { search } from './api.js'

export default function Search({ collections }) {
  const [collection, setCollection] = useState('')
  const [query, setQuery] = useState('')
  const [topK, setTopK] = useState(8)
  const [expand, setExpand] = useState(false)
  const [results, setResults] = useState(null)
  const [resultsExpanded, setResultsExpanded] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSearch(event) {
    event.preventDefault()
    if (!collection || !query.trim()) {
      setError('Pick a collection and enter a query.')
      return
    }
    setError('')
    setBusy(true)
    setResults(null)
    try {
      const data = await search({ collection, query: query.trim(), top_k: Number(topK) || 8, expand })
      setResults(data.results || [])
      setResultsExpanded(expand)
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      {error && <div className="banner error">{error}</div>}

      <section className="card">
        <h2>Search</h2>
        <form onSubmit={onSearch}>
          <div className="field">
            <select value={collection} onChange={(e) => setCollection(e.target.value)}>
              <option value="">— choose a collection —</option>
              {collections.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name} ({c.points} pts · dim {c.dimension})
                </option>
              ))}
            </select>
          </div>
          <div className="field row">
            <input
              type="text"
              placeholder="Ask a question…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              autoFocus
            />
            <input
              className="topk"
              type="number"
              min="1"
              max="50"
              value={topK}
              onChange={(e) => setTopK(e.target.value)}
              title="results to return (top_k)"
            />
            <button className="primary" type="submit" disabled={busy}>
              {busy ? 'Searching…' : 'Search'}
            </button>
          </div>
          <label className="checkbox">
            <input type="checkbox" checked={expand} onChange={(e) => setExpand(e.target.checked)} />
            Expand each hit to its full section (parent-document retrieval)
          </label>
        </form>
      </section>

      {results && (
        <section className="card">
          <h2>{results.length} result{results.length === 1 ? '' : 's'}</h2>
          {results.length === 0 && <div className="hint">No matches.</div>}
          <ol className="results">
            {results.map((r) => (
              <li key={r.id}>
                <div className="result-head">
                  <span className="result-title">{r.metadata?.title || '(untitled)'}</span>
                  <span className="result-score">{r.score.toFixed(3)}</span>
                </div>
                {r.metadata?.heading_path && <div className="result-path">{r.metadata.heading_path}</div>}
                <p className={`result-doc${resultsExpanded ? ' full' : ''}`}>{r.document}</p>
              </li>
            ))}
          </ol>
        </section>
      )}
    </>
  )
}
