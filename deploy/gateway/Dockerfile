# The LAN gateway: the Admin Portal, the client downloads, and a reverse
# proxy to platform-api, on one port. Built from the repository root
# (docker-compose.yml sets the context) because it packages three parts of
# the repo. See docs/LAN_Deployment_Standard.md.

FROM node:22-alpine AS portal
WORKDIR /portal
COPY apps/admin-portal/package.json apps/admin-portal/package-lock.json ./
RUN npm ci
COPY apps/admin-portal/ ./
# Same origin as the gateway: the browser calls /api/..., so no CORS.
ENV VITE_PLATFORM_API_BASE_URL=/api
RUN npm run build

FROM python:3.12-alpine AS client
WORKDIR /build
COPY services/mcp-server/ ./mcp-server/
COPY company-deployment-skill/ ./company-deployment-skill/
COPY deploy/gateway/client/ ./client/
RUN pip wheel --no-deps --wheel-dir /downloads ./mcp-server \
 && python - <<'PY'
import pathlib, zipfile
skill = pathlib.Path("company-deployment-skill")
with zipfile.ZipFile("/downloads/company-deployment-skill.zip", "w", zipfile.ZIP_DEFLATED) as z:
    for p in sorted(skill.rglob("*")):
        if p.is_file() and not ({"node_modules", "__pycache__"} & set(p.parts)):
            z.write(p, p.relative_to(skill).as_posix())
wheel = next(pathlib.Path("/downloads").glob("company_deployment_mcp-*.whl")).name
for name in ("install.ps1", "install.sh"):
    text = pathlib.Path("client", name).read_text(encoding="utf-8").replace("__WHEEL__", wheel)
    pathlib.Path("/downloads", name).write_text(text, encoding="utf-8", newline="\r\n" if name.endswith(".ps1") else "\n")
PY

FROM nginx:1.30-alpine
COPY deploy/gateway/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=portal /portal/dist /usr/share/nginx/html
COPY --from=client /downloads /usr/share/nginx/downloads
