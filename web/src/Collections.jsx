import { useState } from 'react'
import { createCollection, deleteCollection } from './api.js'

export default function Collections({ collections, onChange, selected, onSelect }) {
  const [newName, setNewName] = useState('')
  const [deletingName, setDeletingName] = useState('') // collection awaiting delete confirmation
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onCreate() {
    setError('')
    const name = newName.trim()
    if (!name) return
    setBusy(true)
    try {
      await createCollection(name)
      setNewName('')
      await onChange()
      onSelect?.(name) // make the new collection the active selection everywhere
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  function startDelete(name) {
    setError('')
    setDeletingName(name)
  }

  function cancelDelete() {
    setDeletingName('')
  }

  async function onConfirmDelete(name) {
    setBusy(true)
    try {
      await deleteCollection(name)
      cancelDelete()
      await onChange()
      if (name === selected) onSelect?.('') // clear selection if we deleted it
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
        <h2>Create collection</h2>
        <div className="field row">
          <input
            type="text"
            placeholder="go-style-guide"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && onCreate()}
          />
          <button className="primary" onClick={onCreate} disabled={!newName.trim() || busy}>
            Create
          </button>
        </div>
        <div className="hint">Creates an empty collection sized for the active embedding model.</div>
      </section>

      <section className="card">
        <h2>Collections ({collections.length})</h2>
        {collections.length === 0 ? (
          <div className="hint">No collections yet. Create one above, then ingest from the Ingest tab.</div>
        ) : (
          <ul className="collection-list">
            {collections.map((c) => (
              <li key={c.name} className="collection-row">
                <div className="collection-head">
                  <div className="collection-info">
                    <span className="collection-name">{c.name}</span>
                    <span className="collection-meta">{c.points} pts · dim {c.dimension}</span>
                  </div>
                  {deletingName !== c.name && (
                    <button className="danger-outline" onClick={() => startDelete(c.name)} disabled={busy}>
                      Delete
                    </button>
                  )}
                </div>

                {deletingName === c.name && (
                  <div className="confirm-box">
                    <div className="hint">
                      Permanently delete <strong>{c.name}</strong> and all its points?
                    </div>
                    <div className="field row">
                      <button className="danger" autoFocus disabled={busy} onClick={() => onConfirmDelete(c.name)}>
                        Delete
                      </button>
                      <button onClick={cancelDelete} disabled={busy}>
                        Cancel
                      </button>
                    </div>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  )
}
