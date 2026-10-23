// Where the API service lives. On the platform this page is served at
// /run/{application}/frontend/ and the API at /run/{application}/api/, so
// the API's address is derived from the page's own — nothing to configure
// per environment. Locally (`npm run dev`) Vite proxies /api to the API.
function apiBase() {
  if (import.meta.env.VITE_API_BASE) return import.meta.env.VITE_API_BASE
  const onPlatform = window.location.pathname.match(/^\/run\/[^/]+\//)
  return onPlatform ? `${onPlatform[0]}api` : '/api'
}

export const API_BASE = apiBase()

async function request(path, options = {}) {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) throw new Error(`${options.method || 'GET'} ${path} failed: ${res.status}`)
  return res.json()
}

export const getStatus = () => request('/')
export const listItems = () => request('/items')
export const createItem = (title) =>
  request('/items', { method: 'POST', body: JSON.stringify({ title }) })
