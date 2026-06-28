import { useEffect, useState } from 'react'
import { previewSource, listCollections, createCollection, startIngest } from './api.js'

const TERMINAL = ['succeeded', 'failed']

export default function App() {
  const [sourceType, setSourceType] = useState('upload')
  const [fileName, setFileName] = useState('')
  const [fileContent, setFileContent] = useState('')
  const [githubUrl, setGithubUrl] = useState('')

  const [preview, setPreview] = useState(null)
  const [collections, setCollections] = useState([])
  const [targetMode, setTargetMode] = useState('existing')
  const [selectedCollection, setSelectedCollection] = useState('')
  const [newCollectionName, setNewCollectionName] = useState('')

  const [job, setJob] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    refreshCollections()
  }, [])

  async function refreshCollections() {
    try {
      const data = await listCollections()
      setCollections(data.collections || [])
    } catch (err) {
      setError(`Could not load collections: ${err.message}`)
    }
  }

  function buildSource() {
    if (sourceType === 'upload') {
      return { type: 'upload', name: fileName, content: fileContent }
    }
    return { type: 'github_raw', url: githubUrl.trim() }
  }

  function onFileChange(event) {
    const file = event.target.files?.[0]
    if (!file) return
    setFileName(file.name)
    const reader = new FileReader()
    reader.onload = () => setFileContent(String(reader.result || ''))
    reader.readAsText(file)
  }

  async function onPreview() {
    setError('')
    setPreview(null)
    setBusy(true)
    try {
      setPreview(await previewSource(buildSource()))
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function onCreateCollection() {
    setError('')
    try {
      const created = await createCollection(newCollectionName.trim())
      await refreshCollections()
      setTargetMode('existing')
      setSelectedCollection(created.name)
      setNewCollectionName('')
    } catch (err) {
      setError(err.message)
    }
  }

  function targetCollection() {
    return targetMode === 'existing' ? selectedCollection : newCollectionName.trim()
  }

  async function onIngest() {
    setError('')
    setJob(null)
    const collection = targetCollection()
    if (!collection) {
      setError('Pick or name a collection first.')
      return
    }
    setBusy(true)
    try {
      const { job_id } = await startIngest({ source: buildSource(), collection })
      subscribeJob(job_id)
    } catch (err) {
      setError(err.message)
      setBusy(false)
    }
  }

  function subscribeJob(jobId) {
    const stream = new EventSource(`/api/jobs/${jobId}/stream`)
    stream.onmessage = (event) => {
      const state = JSON.parse(event.data)
      setJob(state)
      if (TERMINAL.includes(state.state)) {
        stream.close()
        setBusy(false)
        refreshCollections()
      }
    }
    stream.onerror = () => {
      stream.close()
      setBusy(false)
    }
  }

  const hasSource = sourceType === 'upload' ? Boolean(fileContent) : Boolean(githubUrl.trim())

  return (
    <main className="app">
      <h1>Markdown → Qdrant</h1>
      <p className="subtitle">Split Markdown on H1 headings and ingest each topic into Qdrant.</p>

      {error && <div className="banner error">{error}</div>}

      <section className="card">
        <h2>1. Source</h2>
        <div className="tabs">
          <button className={sourceType === 'upload' ? 'active' : ''} onClick={() => setSourceType('upload')}>
            Upload .md
          </button>
          <button className={sourceType === 'github_raw' ? 'active' : ''} onClick={() => setSourceType('github_raw')}>
            GitHub raw URL
          </button>
        </div>

        {sourceType === 'upload' ? (
          <div className="field">
            <input type="file" accept=".md,text/markdown" onChange={onFileChange} />
            {fileName && <span className="hint">{fileName} · {fileContent.length} chars</span>}
          </div>
        ) : (
          <div className="field">
            <input
              type="text"
              placeholder="https://raw.githubusercontent.com/owner/repo/main/guide.md"
              value={githubUrl}
              onChange={(e) => setGithubUrl(e.target.value)}
            />
          </div>
        )}

        <button className="primary" disabled={!hasSource || busy} onClick={onPreview}>
          Preview topics
        </button>
      </section>

      {preview && (
        <section className="card">
          <h2>2. Preview — {preview.total_chunks} chunk(s)</h2>
          <div className="hint">{preview.name}</div>
          <ul className="topics">
            {preview.topics.map((topic, i) => (
              <li key={i}>
                <span className="topic-title">{topic.title || '(preamble)'}</span>
                <span className="topic-meta">
                  {topic.chunks} chunk{topic.chunks === 1 ? '' : 's'} · {topic.chars} chars
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="card">
        <h2>3. Destination collection</h2>
        <div className="tabs">
          <button className={targetMode === 'existing' ? 'active' : ''} onClick={() => setTargetMode('existing')}>
            Existing
          </button>
          <button className={targetMode === 'new' ? 'active' : ''} onClick={() => setTargetMode('new')}>
            New
          </button>
        </div>

        {targetMode === 'existing' ? (
          <div className="field">
            <select value={selectedCollection} onChange={(e) => setSelectedCollection(e.target.value)}>
              <option value="">— choose a collection —</option>
              {collections.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name} ({c.points} pts · dim {c.dimension})
                </option>
              ))}
            </select>
          </div>
        ) : (
          <div className="field row">
            <input
              type="text"
              placeholder="go-style-guide"
              value={newCollectionName}
              onChange={(e) => setNewCollectionName(e.target.value)}
            />
            <button onClick={onCreateCollection} disabled={!newCollectionName.trim()}>
              Create
            </button>
          </div>
        )}
      </section>

      <section className="card">
        <h2>4. Ingest</h2>
        <button className="primary" disabled={!hasSource || busy} onClick={onIngest}>
          {busy && job ? 'Ingesting…' : 'Ingest into Qdrant'}
        </button>

        {job && (
          <div className={`job ${job.state}`}>
            <div className="job-state">{job.state}</div>
            {job.total > 0 && (
              <progress value={job.processed} max={job.total} />
            )}
            {job.state === 'succeeded' && <div>Ingested {job.ingested} chunk(s).</div>}
            {job.state === 'failed' && <div className="error">{job.error}</div>}
          </div>
        )}
      </section>
    </main>
  )
}
