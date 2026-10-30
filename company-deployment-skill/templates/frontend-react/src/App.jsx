import { useEffect, useState } from 'react'
import { Link, Route, Routes, useParams, useSearchParams } from 'react-router-dom'
import { createItem, getItem, getStatus, listItems } from './api.js'

const page = { fontFamily: 'system-ui, sans-serif', maxWidth: 640, margin: '2rem auto', padding: '0 1rem' }

export default function App() {
  return (
    <main style={page}>
      <h1>
        <Link to="/">Starter App</Link>
      </h1>
      <Routes>
        <Route path="/" element={<Items />} />
        <Route path="/items/:id" element={<Item />} />
        <Route path="*" element={<p role="alert">Page not found.</p>} />
      </Routes>
    </main>
  )
}

// The list. What it shows comes from the address: ?q= is the search, so
// Back, a refresh or a shared link shows the same results.
function Items() {
  const [searchParams, setSearchParams] = useSearchParams()
  const q = searchParams.get('q') ?? ''
  const [status, setStatus] = useState(null)
  const [items, setItems] = useState([])
  const [search, setSearch] = useState(q)
  const [title, setTitle] = useState('')
  const [error, setError] = useState('')

  async function refresh() {
    try {
      setStatus(await getStatus())
      setItems(await listItems(q))
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }

  useEffect(() => {
    setSearch(q)
    refresh()
  }, [q]) // eslint-disable-line react-hooks/exhaustive-deps

  function onSearch(e) {
    e.preventDefault()
    const next = search.trim()
    setSearchParams(next ? { q: next } : {})
  }

  async function onAdd(e) {
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
    <>
      <p data-testid="status">
        API: {status ? `${status.status} (${status.runtime}, database ${status.database})` : 'loading…'}
      </p>
      {error && <p role="alert" style={{ color: '#b00020' }}>{error}</p>}
      <form onSubmit={onSearch} role="search" style={{ display: 'flex', gap: '0.5rem' }}>
        <input aria-label="Search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search" style={{ flex: 1 }} />
        <button type="submit">Search</button>
      </form>
      <form onSubmit={onAdd} style={{ display: 'flex', gap: '0.5rem', marginTop: '0.5rem' }}>
        <input aria-label="New item" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="New item" style={{ flex: 1 }} />
        <button type="submit">Add</button>
      </form>
      <ul>
        {items.map((item) => (
          <li key={item.id}>
            <Link to={`/items/${item.id}`}>{item.title}</Link>
          </li>
        ))}
      </ul>
    </>
  )
}

// One item, at its own address: /items/{id} opens directly.
function Item() {
  const { id } = useParams()
  const [item, setItem] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    getItem(id).then(setItem, (e) => setError(e.message))
  }, [id])

  if (error) return <p role="alert" style={{ color: '#b00020' }}>{error}</p>
  if (!item) return <p>loading…</p>
  return (
    <article>
      <h2 data-testid="item-title">{item.title}</h2>
      <p>Item {item.id}</p>
      <Link to="/">All items</Link>
    </article>
  )
}
