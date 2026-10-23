// Where the API service lives. On the platform this page is served at
// /{application}/ and the API at /{application}/api/ — or, in the long form,
// /run/{application}/frontend/ and /run/{application}/api/ — so the API's
// address is derived from the page's own: nothing to configure per
// environment. Locally (`npm run dev`) Vite proxies /api to the API.
function apiBase() {
  if (import.meta.env.VITE_API_BASE) return import.meta.env.VITE_API_BASE
  if (import.meta.env.DEV) return '/api'
  const path = window.location.pathname
  const longForm = path.match(/^\/run\/[^/]+\//)
  if (longForm) return `${longForm[0]}api`
  const app = path.match(/^\/[^/]+\//)
  return app ? `${app[0]}api` : '/api'
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
