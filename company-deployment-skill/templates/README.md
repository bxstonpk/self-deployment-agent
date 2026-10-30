# Starter templates — the company LAN development standard

Every new application on the platform starts from the same shape:

| Part | Choice | Template |
|---|---|---|
| Frontend | React (Vite) | `frontend-react/` |
| API | one of Python, JavaScript (Node.js), TypeScript, Go | `api-python/`, `api-nodejs/`, `api-typescript/`, `api-go/` |
| Database | MySQL 8.4, provisioned by the platform | declared in `deployment.yaml` |
| Review document | what the approver reads alongside every file: workflow, structure and paths, data, what changed | `REVIEW.md`, at the project root, required for an environment that waits for approval |

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
  `frontend-react/index.html` sets `<base>` to the app's own root before
  anything loads, `src/base.js` exposes it, and `src/api.js` derives the
  API address from it. Vite builds with relative asset paths
  (`base: './'`), so nothing is configured per environment and a page at
  any depth (`/<app>/items/7`) loads.
- **Example endpoints.** `GET /items` (`?q=` searches), `GET /items/{id}`
  and `POST /items {"title": …}`. Replace them with the application's own,
  in the same RESTful shape.

## The API is RESTful

The company standard (`DEC-032`):

- **Resources are nouns.** `/items` is the collection and `/items/{id}`
  one item. Name them for what they are (`/leave-requests`,
  `/leave-requests/{id}/approvals`), not for an action (`/getLeave`,
  `/doApprove`).
- **The method says what happens.**
  - `GET` reads and never changes anything.
  - `POST` creates.
  - `PUT` replaces; `PATCH` changes part.
  - `DELETE` removes.
- **JSON in and out.** Status codes mean what they say: `200` ok, `201`
  created, `204` nothing to return, `400` bad input, `404` not found,
  `409` conflict, `500` a fault.
- **Searching, filtering, sorting and paging are query parameters on the
  collection.** For example: `GET /items?q=pen&status=open&sort=-created_at&page=2`.

## Pages and the address

Every page has its own address, and what a page shows is in that address,
so **Back, a refresh or a link someone shares opens exactly the same
thing**:

- **Pages are paths.** Use `/<app>/items/7`, never hash routes like
  `/<app>/#/items/7`.
  - The template uses React Router's `BrowserRouter`, with
    `basename={ROUTER_BASENAME}` from `src/base.js`.
  - The platform refuses hash routing at build time, and `check_project`
    reports it too. The refused APIs are `HashRouter`, `createHashRouter`,
    Vue's `createWebHashHistory` and `createHashHistory`.
- **The page's state is in the query.** Search, filters, sort, page and
  the open tab go in the query (`?q=pen&status=open&page=2`).
  - Read them with `useSearchParams` when the page opens, as `Items` in
    `src/App.jsx` does, and pass them to the API as the same query
    parameters.
  - Changing a search or a filter changes the address
    (`setSearchParams`). Don't keep it only in component state.
- **Links are router links.** Use `<Link to="/items/7">`, relative to the
  app, never a hard-coded `/<app>/` prefix: the same build serves the
  app's `-dev` copy and its long-form address.


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
the API working. Each API's `?q=` search, `GET /items/{id}` and its 404
are checked, and a deep link (`/<app>/items/1?q=x`) is served the
frontend. A real browser then drives one frontend end to end:
- it adds an item;
- opening `?q=` directly lists only the match;
- an item opens at a path with no `#`, and a reload keeps it;
- Back returns to the same search.
