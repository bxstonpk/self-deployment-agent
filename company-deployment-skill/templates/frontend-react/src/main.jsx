import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App.jsx'
import { ROUTER_BASENAME } from './base.js'

// Path routes (/items/7), never hash routes (/#/items/7): the company
// standard, which the platform's build enforces.
createRoot(document.getElementById('root')).render(
  <StrictMode>
    <BrowserRouter basename={ROUTER_BASENAME}>
      <App />
    </BrowserRouter>
  </StrictMode>,
)
