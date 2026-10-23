// Starter API — Node.js (JavaScript, Express) on the platform's MySQL.
//
// The platform runs `npm start`, injects PORT (the port it routes to) and,
// because deployment.yaml declares `database: type: mysql`, the connection
// details as DATABASE_HOST / DATABASE_PORT / DATABASE_NAME / DATABASE_USER /
// DATABASE_PASSWORD. Nothing here is configured by hand.
import express from 'express'
import mysql from 'mysql2/promise'

const pool = mysql.createPool({
  host: process.env.DATABASE_HOST || '127.0.0.1',
  port: Number(process.env.DATABASE_PORT || 3306),
  user: process.env.DATABASE_USER || 'appuser',
  password: process.env.DATABASE_PASSWORD || '',
  database: process.env.DATABASE_NAME || 'appdb',
  connectionLimit: 5,
})

let ready = false

// The database can still be starting when the API does; keep trying rather
// than exit, so the API is healthy either way.
async function initSchema() {
  while (!ready) {
    try {
      await pool.query(
        `CREATE TABLE IF NOT EXISTS items (
           id INT AUTO_INCREMENT PRIMARY KEY,
           title VARCHAR(200) NOT NULL,
           created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
      )
      ready = true
    } catch (err) {
      console.log(`database not ready yet: ${err.message}`)
      await new Promise((r) => setTimeout(r, 2000))
    }
  }
}

const app = express()
app.use(express.json())

app.get('/', (req, res) => {
  res.json({ status: 'ok', runtime: 'nodejs', database: ready ? 'connected' : 'unavailable' })
})

app.get('/items', async (req, res, next) => {
  try {
    const [rows] = await pool.query('SELECT id, title, created_at FROM items ORDER BY id')
    res.json(rows)
  } catch (err) {
    next(err)
  }
})

app.post('/items', async (req, res, next) => {
  const title = String(req.body?.title ?? '').trim().slice(0, 200)
  if (!title) return res.status(400).json({ error: 'title is required' })
  try {
    const [result] = await pool.query('INSERT INTO items (title) VALUES (?)', [title])
    res.status(201).json({ id: result.insertId, title })
  } catch (err) {
    next(err)
  }
})

const port = Number(process.env.PORT || 8080)
app.listen(port, () => console.log(`listening on :${port}`))
initSchema()
