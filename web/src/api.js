async function jsonFetch(url, options) {
  const res = await fetch(url, options)
  const text = await res.text()
  const data = text ? JSON.parse(text) : {}
  if (!res.ok) {
    throw new Error(data.error || `request failed (${res.status})`)
  }
  return data
}

export function previewSource(source) {
  return jsonFetch('/api/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source }),
  })
}

export function listCollections() {
  return jsonFetch('/api/collections')
}

export function listHeadings(collection) {
  return jsonFetch(`/api/collections/${encodeURIComponent(collection)}/headings`)
}

export function createCollection(name) {
  return jsonFetch('/api/collections', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

export function startIngest(payload) {
  return jsonFetch('/api/ingest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function search(payload) {
  return jsonFetch('/api/search', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function evaluate(payload) {
  return jsonFetch('/api/eval', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}
