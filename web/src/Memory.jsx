import { useEffect, useState } from 'react'
import { listMemories, remember, forgetMemory } from './api.js'

const DEFAULT_THRESHOLD = 0.97

export default function Memory({ collections, collection, onCollection }) {
  const [text, setText] = useState('')
  const [author, setAuthor] = useState('')
  const [tags, setTags] = useState('')
  const [namespace, setNamespace] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [threshold, setThreshold] = useState(DEFAULT_THRESHOLD)

  const [memories, setMemories] = useState([])
  const [result, setResult] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setResult(null)
    setError('')
    if (collection) {
      refreshMemories()
    } else {
      setMemories([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collection])

  async function refreshMemories() {
    try {
      const data = await listMemories(collection)
      setMemories(data.memories || [])
    } catch (err) {
      setError(`Could not load memories: ${err.message}`)
    }
  }

  async function onRemember(event) {
    event.preventDefault()
    if (!collection || !text.trim()) {
      setError('Pick a collection and enter a fact to remember.')
      return
    }
    setError('')
    setBusy(true)
    setResult(null)
    try {
      const payload = {
        collection,
        text: text.trim(),
        author: author.trim(),
        namespace: namespace.trim(),
        tags: tags.trim(),
      }
      if (advanced) {
        payload.supersede_threshold = Number(threshold)
      }
      const data = await remember(payload)
      setResult(data)
      setText('')
      await refreshMemories()
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function onForget(id) {
    setError('')
    try {
      await forgetMemory(collection, id)
      await refreshMemories()
    } catch (err) {
      setError(err.message)
    }
  }

  return (
    <>
      {error && <div className="banner error">{error}</div>}

      <section className="card">
        <h2>Remember a fact</h2>
        <form onSubmit={onRemember}>
          <div className="field">
            <select value={collection} onChange={(e) => onCollection(e.target.value)}>
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
              rows={3}
              placeholder="A short, self-contained fact — e.g. “Acme is on the legacy billing plan.”"
              value={text}
              onChange={(e) => setText(e.target.value)}
            />
          </div>
          <div className="field row">
            <input type="text" placeholder="author (optional)" value={author} onChange={(e) => setAuthor(e.target.value)} />
            <input type="text" placeholder="namespace (optional)" value={namespace} onChange={(e) => setNamespace(e.target.value)} />
            <input type="text" placeholder="tags (optional)" value={tags} onChange={(e) => setTags(e.target.value)} />
            <button className="primary" type="submit" disabled={busy}>
              {busy ? 'Saving…' : 'Remember'}
            </button>
          </div>
          <label className="checkbox">
            <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} />
            Advanced: tune how aggressively similar memories are merged
          </label>
          {advanced && (
            <div className="field row">
              <input
                type="range"
                min="0.5"
                max="1"
                step="0.01"
                value={threshold}
                onChange={(e) => setThreshold(e.target.value)}
                title="supersede threshold"
              />
              <span className="hint">
                merge threshold {Number(threshold).toFixed(2)} — lower merges more (risk of overwriting distinct facts);
                higher keeps more separate. Default {DEFAULT_THRESHOLD}.
              </span>
            </div>
          )}
        </form>
        {result && (
          <div className="banner">
            {result.superseded
              ? `Updated an existing memory (v${result.version}) — ${result.source_id}`
              : `Stored a new memory (v${result.version}) — ${result.source_id}`}
          </div>
        )}
      </section>

      {collection && (
        <section className="card">
          <h2>{memories.length} memor{memories.length === 1 ? 'y' : 'ies'} in {collection}</h2>
          {memories.length === 0 && <div className="hint">No memories yet. Remember a fact above.</div>}
          <ol className="results">
            {memories.map((m) => (
              <li key={m.source_id}>
                <div className="result-head">
                  <span className="result-title">{m.author || '(anonymous)'}{m.tags ? ` · ${m.tags}` : ''}</span>
                  <button className="danger-outline" onClick={() => onForget(m.source_id)} title="forget this memory">
                    forget
                  </button>
                </div>
                {m.updated_at && <div className="result-path">v{m.version} · {m.updated_at}</div>}
                <p className="result-doc full">{m.document}</p>
              </li>
            ))}
          </ol>
        </section>
      )}
    </>
  )
}
