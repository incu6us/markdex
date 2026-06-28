import { useState } from 'react'
import { evaluate } from './api.js'

const DEFAULT_GOLDEN = `{
  "top_k": 10,
  "queries": [
    { "query": "how should I name variables and functions", "relevant_heading_contains": ["naming"] },
    { "query": "tell me about interfaces in Go", "relevant_heading_contains": ["interfaces"] },
    { "query": "how do I handle and wrap errors", "relevant_heading_contains": ["error-handling"] },
    { "query": "table driven tests and test helpers", "relevant_heading_contains": ["tests"] },
    { "query": "managing global state and package-level variables", "relevant_heading_contains": ["global-state"] },
    { "query": "defining a contract that multiple types can satisfy", "relevant_heading_contains": ["interfaces"] }
  ]
}`

export default function Eval({ collections }) {
  const [collection, setCollection] = useState('')
  const [golden, setGolden] = useState(DEFAULT_GOLDEN)
  const [report, setReport] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onRun() {
    setError('')
    setReport(null)
    if (!collection) {
      setError('Pick a collection.')
      return
    }

    let set
    try {
      set = JSON.parse(golden)
    } catch (err) {
      setError(`Golden set is not valid JSON: ${err.message}`)
      return
    }
    if (!set.queries || set.queries.length === 0) {
      setError('Golden set has no queries.')
      return
    }

    setBusy(true)
    try {
      const data = await evaluate({ collection, top_k: set.top_k || 10, queries: set.queries })
      setReport(data)
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  const m = report?.metrics

  return (
    <>
      {error && <div className="banner error">{error}</div>}

      <section className="card">
        <h2>Evaluate retrieval</h2>
        <p className="hint">
          Posts the golden set to <code>/api/eval</code>, which runs each query through search
          and scores how highly the expected section ranks. A result is relevant if its{' '}
          <code>heading_path</code> contains one of <code>relevant_heading_contains</code>.
        </p>

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

        <div className="field">
          <textarea
            className="golden"
            spellCheck={false}
            value={golden}
            onChange={(e) => setGolden(e.target.value)}
            rows={12}
          />
        </div>

        <button className="primary" onClick={onRun} disabled={busy}>
          {busy ? 'Running…' : 'Run eval'}
        </button>
      </section>

      {report && (
        <section className="card">
          <h2>Results</h2>
          <div className="metrics">
            <div className="metric"><span className="metric-value">{m.mrr.toFixed(3)}</span><span className="metric-label">MRR</span></div>
            <div className="metric"><span className="metric-value">{(m.hit_at_1 * 100).toFixed(0)}%</span><span className="metric-label">Hit@1</span></div>
            <div className="metric"><span className="metric-value">{(m.hit_at_3 * 100).toFixed(0)}%</span><span className="metric-label">Hit@3</span></div>
            <div className="metric"><span className="metric-value">{(m.hit_at_k * 100).toFixed(0)}%</span><span className="metric-label">Hit@{report.top_k}</span></div>
          </div>

          <ul className="topics">
            {report.results.map((row, i) => (
              <li key={i}>
                <span className="topic-title">{row.query}</span>
                <span className={`topic-meta ${row.rank === 0 ? 'miss' : ''}`}>
                  {row.rank === 0 ? 'MISS' : `rank ${row.rank}`}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  )
}
