# Starter templates — the company LAN development standard

Every new application on the platform starts from the same shape:

| Part | Choice | Template |
|---|---|---|
| Frontend | React (Vite) | `frontend-react/` |
| API | one of Python, JavaScript (Node.js), TypeScript, Go | `api-python/`, `api-nodejs/`, `api-typescript/`, `api-go/` |
| Database | MySQL 8.4, provisioned by the platform | declared in `deployment.yaml` |

## Assembling a project

```
my-app/
  deployment.yaml      ← templates/deployment.yaml, with the <...> filled in
  frontend/            ← templates/frontend-react/
  api/                 ← templates/api-<runtime>/
```

The directory names matter: the platform builds each top-level directory
as the service of the same name in `deployment.yaml`'s `services:`. Set
`services.api.runtime` to `python`, `nodejs`, `typescript` or `go` to match
the API you copied.

## What the templates already do

Each one follows the platform's conventions, so it deploys without
changes:

- **Build recipe.** `react`: `npm run build` → `build/`, served by
  nginx. `nodejs`: production dependencies, then the `start` script.
  `typescript`: `npm run build` (tsc → `dist/`), devDependencies pruned,
  then the `start` script. `python`: `pip install -r requirements.txt`,
  then `python app.py`. `go`: `go build` at the service root. The
  platform runs a `start` script with `node_modules/.bin` on `PATH` but
  **without npm** (runtime images carry no package manager), so a start
  script must not call `npm` itself.
- **Port.** The platform sets `PORT` to the port declared in
  `deployment.yaml` (8080 in the template); every API listens on it.
- **Database.** The platform injects `DATABASE_HOST`, `DATABASE_PORT`,
  `DATABASE_NAME`, `DATABASE_USER`, `DATABASE_PASSWORD` (plus
  `DATABASE_URL` and `DATABASE_ENGINE`). Every API reads those and creates
  its `items` table at start-up, retrying until MySQL accepts connections.
  No credential is ever written into source or `deployment.yaml`.
- **Health.** `GET /` on every API answers `{"status": "ok", "runtime": …,
  "database": "connected" | "unavailable"}`. The platform's health check
  polls `/`.
- **Addresses.** Deployed, the frontend is served at `/<app>/` and the
  API at `/<app>/api/` (the long form, `/run/<app>/frontend/` and
  `/run/<app>/api/`, works too).
  `frontend-react/src/api.js` derives the API address from the page's own,
  and Vite builds with relative asset paths (`base: './'`), so nothing is
  configured per environment.
- **Example endpoints.** `GET /items` and `POST /items {"title": …}` —
  replace them with the application's own.

## Running locally

```
docker compose -f docker-compose.dev.yml up -d     # a local MySQL 8.4
cd api && <run the API with DATABASE_PASSWORD=devpassword>
cd frontend && npm install && npm run dev          # proxies /api to localhost:8080
```

How to run each API:

| Runtime | Command (from `api/`) |
|---|---|
| python | `pip install -r requirements.txt && python app.py` |
| nodejs | `npm install && npm start` |
| typescript | `npm install && npm run build && npm start` |
| go | `go run .` |

`docker-compose.dev.yml` is for your machine only. It is never sent to the
platform, which provisions its own database.

## How these were verified

`services/platform-api/scripts/verify_lan_stack.py` builds and deploys
React + each of the four APIs + MySQL from exactly these files on a live
platform. For each, it checks that the API reaches MySQL, that data
written through the platform's address comes back, and that the frontend
and its bundle load. It also checks that the database is isolated on the
application's own network, and that rotating the database password keeps
the API working. A real browser then drives one frontend end to end.
