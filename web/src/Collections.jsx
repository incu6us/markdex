import { useState } from 'react'
import { createCollection, deleteCollection } from './api.js'

export default function Collections({ collections, onChange }) {
  const [newName, setNewName] = useState('')
  const [deletingName, setDeletingName] = useState('') // collection awaiting type-to-confirm
  const [confirmText, setConfirmText] = useState('')
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
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  function startDelete(name) {
    setError('')
    setDeletingName(name)
    setConfirmText('')
  }

  function cancelDelete() {
    setDeletingName('')
    setConfirmText('')
  }

  async function onConfirmDelete(name) {
    setBusy(true)
    try {
      await deleteCollection(name)
      cancelDelete()
      await onChange()
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
                      Type <strong>{c.name}</strong> to permanently delete this collection and all its points.
                    </div>
                    <div className="field row">
                      <input
                        type="text"
                        value={confirmText}
                        autoFocus
                        placeholder={c.name}
                        onChange={(e) => setConfirmText(e.target.value)}
                        onKeyDown={(e) => e.key === 'Enter' && confirmText === c.name && onConfirmDelete(c.name)}
                      />
                      <button className="danger" disabled={confirmText !== c.name || busy} onClick={() => onConfirmDelete(c.name)}>
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
