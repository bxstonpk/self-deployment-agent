// Starter API — TypeScript (Express) on the platform's MySQL.
//
// The platform runs `npm run build` (tsc -> dist/) when it builds the image,
// then `npm start`. It injects PORT (the port it routes to) and, because
// deployment.yaml declares `database: type: mysql`, the connection details
// as DATABASE_HOST / DATABASE_PORT / DATABASE_NAME / DATABASE_USER /
// DATABASE_PASSWORD. Nothing here is configured by hand.
import express, { type NextFunction, type Request, type Response } from 'express'
import mysql, { type ResultSetHeader, type RowDataPacket } from 'mysql2/promise'

const pool = mysql.createPool({
  host: process.env.DATABASE_HOST ?? '127.0.0.1',
  port: Number(process.env.DATABASE_PORT ?? 3306),
  user: process.env.DATABASE_USER ?? 'appuser',
  password: process.env.DATABASE_PASSWORD ?? '',
  database: process.env.DATABASE_NAME ?? 'appdb',
  connectionLimit: 5,
})

let ready = false

// The database can still be starting when the API does; keep trying rather
// than exit, so the API is healthy either way.
async function initSchema(): Promise<void> {
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
      console.log(`database not ready yet: ${(err as Error).message}`)
      await new Promise((r) => setTimeout(r, 2000))
    }
  }
}

const app = express()
app.use(express.json())

app.get('/', (_req: Request, res: Response) => {
  res.json({ status: 'ok', runtime: 'typescript', database: ready ? 'connected' : 'unavailable' })
})

// Matches q anywhere, with LIKE's own wildcards taken literally.
const likePattern = (q: string): string => `%${q.replace(/[\\%_]/g, (c) => `\\${c}`)}%`

// RESTful: the collection is /items, one item /items/:id, and a search is a
// query parameter on the collection (GET /items?q=...).
app.get('/items', async (req: Request, res: Response, next: NextFunction) => {
  const q = String(req.query.q ?? '').trim()
  try {
    const [rows] = q
      ? await pool.query<RowDataPacket[]>('SELECT id, title, created_at FROM items WHERE title LIKE ? ORDER BY id', [likePattern(q)])
      : await pool.query<RowDataPacket[]>('SELECT id, title, created_at FROM items ORDER BY id')
    res.json(rows)
  } catch (err) {
    next(err)
  }
})

app.get('/items/:id', async (req: Request, res: Response, next: NextFunction) => {
  const id = Number(req.params.id)
  if (!Number.isInteger(id)) {
    res.status(404).json({ error: 'item not found' })
    return
  }
  try {
    const [rows] = await pool.query<RowDataPacket[]>('SELECT id, title, created_at FROM items WHERE id = ?', [id])
    if (rows.length === 0) {
      res.status(404).json({ error: 'item not found' })
      return
    }
    res.json(rows[0])
  } catch (err) {
    next(err)
  }
})

app.post('/items', async (req: Request, res: Response, next: NextFunction) => {
  const title = String(req.body?.title ?? '').trim().slice(0, 200)
  if (!title) {
    res.status(400).json({ error: 'title is required' })
    return
  }
  try {
    const [result] = await pool.query<ResultSetHeader>('INSERT INTO items (title) VALUES (?)', [title])
    res.status(201).json({ id: result.insertId, title })
  } catch (err) {
    next(err)
  }
})

const port = Number(process.env.PORT ?? 8080)
app.listen(port, () => console.log(`listening on :${port}`))
void initSchema()
