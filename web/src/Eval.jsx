import { useState } from 'react'
import { search } from './api.js'

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

function isRelevant(headingPath, contains) {
  const hp = (headingPath || '').toLowerCase()
  return (contains || []).some((w) => w && hp.includes(w.toLowerCase()))
}

function firstRelevantRank(paths, contains) {
  for (let i = 0; i < paths.length; i++) {
    if (isRelevant(paths[i], contains)) return i + 1
  }
  return 0
}

function aggregate(ranks, k) {
  const n = ranks.length || 1
  let hit1 = 0, hit3 = 0, hitK = 0, rr = 0
  for (const r of ranks) {
    if (!r) continue
    rr += 1 / r
    if (r === 1) hit1++
    if (r <= 3) hit3++
    if (r <= k) hitK++
  }
  return { mrr: rr / n, hit1: hit1 / n, hit3: hit3 / n, hitK: hitK / n }
}

export default function Eval({ collections }) {
  const [collection, setCollection] = useState('')
  const [golden, setGolden] = useState(DEFAULT_GOLDEN)
  const [rows, setRows] = useState(null)
  const [metrics, setMetrics] = useState(null)
  const [k, setK] = useState(10)
  const [progress, setProgress] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onRun() {
    setError('')
    setRows(null)
    setMetrics(null)
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
    const queries = set.queries || []
    const topK = set.top_k || 10
    setK(topK)
    if (queries.length === 0) {
      setError('Golden set has no queries.')
      return
    }

    setBusy(true)
    const ranks = []
    const perQuery = []
    try {
      for (let i = 0; i < queries.length; i++) {
        setProgress(`${i + 1}/${queries.length}`)
        const data = await search({ collection, query: queries[i].query, top_k: topK })
        const paths = (data.results || []).map((r) => r.metadata?.heading_path || '')
        const rank = firstRelevantRank(paths, queries[i].relevant_heading_contains)
        ranks.push(rank)
        perQuery.push({ query: queries[i].query, rank })
      }
      setRows(perQuery)
      setMetrics(aggregate(ranks, topK))
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
      setProgress('')
    }
  }

  return (
    <>
      {error && <div className="banner error">{error}</div>}

      <section className="card">
        <h2>Evaluate retrieval</h2>
        <p className="hint">
          Runs each golden query against <code>/api/search</code> and scores how highly the
          expected section ranks. A result is relevant if its <code>heading_path</code>
          contains one of <code>relevant_heading_contains</code>.
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
          {busy ? `Running ${progress}…` : 'Run eval'}
        </button>
      </section>

      {metrics && (
        <section className="card">
          <h2>Results</h2>
          <div className="metrics">
            <div className="metric"><span className="metric-value">{metrics.mrr.toFixed(3)}</span><span className="metric-label">MRR</span></div>
            <div className="metric"><span className="metric-value">{(metrics.hit1 * 100).toFixed(0)}%</span><span className="metric-label">Hit@1</span></div>
            <div className="metric"><span className="metric-value">{(metrics.hit3 * 100).toFixed(0)}%</span><span className="metric-label">Hit@3</span></div>
            <div className="metric"><span className="metric-value">{(metrics.hitK * 100).toFixed(0)}%</span><span className="metric-label">Hit@{k}</span></div>
          </div>

          <ul className="topics">
            {rows.map((row, i) => (
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
