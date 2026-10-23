import { useEffect, useState } from 'react'
import { createItem, getStatus, listItems } from './api.js'

export default function App() {
  const [status, setStatus] = useState(null)
  const [items, setItems] = useState([])
  const [title, setTitle] = useState('')
  const [error, setError] = useState('')

  async function refresh() {
    try {
      setStatus(await getStatus())
      setItems(await listItems())
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }

  useEffect(() => {
    refresh()
  }, [])

  async function onSubmit(e) {
    e.preventDefault()
    if (!title.trim()) return
    try {
      await createItem(title.trim())
      setTitle('')
      await refresh()
    } catch (e) {
      setError(e.message)
    }
  }

  return (
    <main style={{ fontFamily: 'system-ui, sans-serif', maxWidth: 640, margin: '2rem auto', padding: '0 1rem' }}>
      <h1>Starter App</h1>
      <p data-testid="status">
        API: {status ? `${status.status} (${status.runtime}, database ${status.database})` : 'loading…'}
      </p>
      {error && <p role="alert" style={{ color: '#b00020' }}>{error}</p>}
      <form onSubmit={onSubmit} style={{ display: 'flex', gap: '0.5rem' }}>
        <input
          aria-label="New item"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="New item"
          style={{ flex: 1 }}
        />
        <button type="submit">Add</button>
      </form>
      <ul>
        {items.map((item) => (
          <li key={item.id}>{item.title}</li>
        ))}
      </ul>
    </main>
  )
}
