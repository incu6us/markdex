import { useEffect, useState } from 'react'
import { previewSource, listCollections, createCollection, startIngest } from './api.js'
import usePersistedState from './usePersistedState.js'
import Search from './Search.jsx'
import Eval from './Eval.jsx'
import Collections from './Collections.jsx'

const TERMINAL = ['succeeded', 'failed']

export default function App() {
  // Persisted across tab switches and reloads.
  const [mode, setMode] = usePersistedState('markdex.tab', 'ingest')
  const [collection, setCollection] = usePersistedState('markdex.collection', '')

  const [sourceType, setSourceType] = useState('upload')
  const [fileName, setFileName] = useState('')
  const [fileContent, setFileContent] = useState('')
  const [githubUrl, setGithubUrl] = useState('')
  const [repoUrl, setRepoUrl] = useState('')
  const [pruneRepo, setPruneRepo] = useState(false)
  const [folderFiles, setFolderFiles] = useState([]) // [{name, content}] from a local folder

  const [preview, setPreview] = useState(null)
  const [collections, setCollections] = useState([])
  const [targetMode, setTargetMode] = useState('existing')
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
    if (sourceType === 'github_repo') {
      return { type: 'github_repo', url: repoUrl.trim() }
    }
    if (sourceType === 'upload_dir') {
      return { type: 'upload_dir', files: folderFiles }
    }
    return { type: 'github_raw', url: githubUrl.trim() }
  }

  async function onFolderChange(event) {
    const all = Array.from(event.target.files || [])
    const mdFiles = all.filter((f) => f.name.toLowerCase().endsWith('.md'))
    try {
      const files = await Promise.all(
        mdFiles.map(async (f) => ({ name: f.webkitRelativePath || f.name, content: await f.text() })),
      )
      setFolderFiles(files)
    } catch (err) {
      setError(`Could not read folder: ${err.message}`)
    }
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
      setCollection(created.name)
      setNewCollectionName('')
    } catch (err) {
      setError(err.message)
    }
  }

  function targetCollection() {
    return targetMode === 'existing' ? collection : newCollectionName.trim()
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
      const prune = sourceType === 'github_repo' && pruneRepo
      const { job_id } = await startIngest({ source: buildSource(), collection, prune })
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

  const hasSource =
    sourceType === 'upload'
      ? Boolean(fileContent)
      : sourceType === 'github_repo'
        ? Boolean(repoUrl.trim())
        : sourceType === 'upload_dir'
          ? folderFiles.length > 0
          : Boolean(githubUrl.trim())
  // Preview reads a single document; a whole repo/folder is ingested directly.
  const canPreview = sourceType === 'upload' || sourceType === 'github_raw'

  return (
    <main className="app">
      <h1>markdex</h1>
      <p className="subtitle">
        Split Markdown on H1 headings, embed with BGE-M3, and search with hybrid retrieval + reranking.
      </p>

      <nav className="nav">
        <button className={mode === 'ingest' ? 'active' : ''} onClick={() => setMode('ingest')}>
          Ingest
        </button>
        <button className={mode === 'search' ? 'active' : ''} onClick={() => setMode('search')}>
          Search
        </button>
        <button className={mode === 'collections' ? 'active' : ''} onClick={() => setMode('collections')}>
          Collections
        </button>
        <button className={mode === 'eval' ? 'active' : ''} onClick={() => setMode('eval')}>
          Eval
        </button>
      </nav>

      {error && <div className="banner error">{error}</div>}

      {mode === 'search' && <Search collections={collections} collection={collection} onCollection={setCollection} />}
      {mode === 'collections' && (
        <Collections collections={collections} onChange={refreshCollections} selected={collection} onSelect={setCollection} />
      )}
      {mode === 'eval' && <Eval collections={collections} collection={collection} onCollection={setCollection} />}

      {mode === 'ingest' && (
        <>
      <section className="card">
        <h2>1. Source</h2>
        <div className="tabs">
          <button className={sourceType === 'upload' ? 'active' : ''} onClick={() => setSourceType('upload')}>
            Upload .md
          </button>
          <button className={sourceType === 'github_raw' ? 'active' : ''} onClick={() => setSourceType('github_raw')}>
            GitHub raw URL
          </button>
          <button className={sourceType === 'github_repo' ? 'active' : ''} onClick={() => setSourceType('github_repo')}>
            GitHub repo
          </button>
          <button className={sourceType === 'upload_dir' ? 'active' : ''} onClick={() => setSourceType('upload_dir')}>
            Local folder
          </button>
        </div>

        {sourceType === 'upload' && (
          <div className="field">
            <input type="file" accept=".md,text/markdown" onChange={onFileChange} />
            {fileName && <span className="hint">{fileName} · {fileContent.length} chars</span>}
          </div>
        )}
        {sourceType === 'github_raw' && (
          <div className="field">
            <input
              type="text"
              placeholder="https://raw.githubusercontent.com/owner/repo/main/guide.md"
              value={githubUrl}
              onChange={(e) => setGithubUrl(e.target.value)}
            />
          </div>
        )}
        {sourceType === 'github_repo' && (
          <div className="field">
            <input
              type="text"
              placeholder="https://github.com/owner/repo  (or owner/repo, or …/tree/branch/path)"
              value={repoUrl}
              onChange={(e) => setRepoUrl(e.target.value)}
            />
            <span className="hint">Ingests every <code>.md</code> in the repo (or subpath). No preview — ingest directly.</span>
            <label className="checkbox">
              <input type="checkbox" checked={pruneRepo} onChange={(e) => setPruneRepo(e.target.checked)} />
              Remove chunks for files deleted from the repo (reconcile)
            </label>
          </div>
        )}
        {sourceType === 'upload_dir' && (
          <div className="field">
            <input type="file" webkitdirectory="" directory="" multiple onChange={onFolderChange} />
            <span className="hint">
              {folderFiles.length > 0
                ? `${folderFiles.length} .md file${folderFiles.length === 1 ? '' : 's'} selected — ingest directly.`
                : 'Pick a folder; every .md in it (recursively) is ingested.'}
            </span>
          </div>
        )}

        {canPreview && (
          <button className="primary" disabled={!hasSource || busy} onClick={onPreview}>
            Preview topics
          </button>
        )}
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
            <select value={collection} onChange={(e) => setCollection(e.target.value)}>
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
        </>
      )}
    </main>
  )
}
