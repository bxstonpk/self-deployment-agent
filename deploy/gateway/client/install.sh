#!/bin/sh
# Sets up this macOS or Linux machine to build and deploy with Claude Code
# on the Company Deployment Platform. Download it from the platform and run:
#
#   curl -fsSL __PLATFORM_URL__/downloads/install.sh | sh
#
# Safe to run again: it upgrades in place. Pass --skip-login to sign in later.
#
# What it does, all under your home directory (no sudo):
#   1. a Python virtual environment in ~/.company-deploy/venv with the
#      Company Deployment MCP server and the `company-deploy` command
#   2. the Company Deployment skill in ~/.claude/skills/company-deployment
#   3. registers the MCP server with Claude Code (user scope)
#   4. signs this machine in: you approve a code in the Admin Portal, and
#      the machine stores its own API key. The key is never shown.
set -eu

PLATFORM_URL="${COMPANY_PLATFORM_URL:-__PLATFORM_URL__}"
PLATFORM_URL="${PLATFORM_URL%/}"
API="$PLATFORM_URL/api"
# Where this script came from. The files are fetched from there too, over
# HTTP when the platform's HTTPS uses the company CA this machine doesn't
# trust yet.
DOWNLOADS="${COMPANY_DOWNLOADS_URL:-__DOWNLOADS_URL__}"
DOWNLOADS="${DOWNLOADS%/}"
# Still the placeholder (a copy not served by the platform)? The pattern
# is split, or the gateway's substitution would replace this check too.
case "$DOWNLOADS" in *__DOWNLOADS*_URL__*) DOWNLOADS="$PLATFORM_URL/downloads" ;; esac
WHEEL="__WHEEL__"
ROOT="$HOME/.company-deploy"
VENV="$ROOT/venv"
SKILL_DIR="$HOME/.claude/skills/company-deployment"
SKIP_LOGIN=0
for arg in "$@"; do
  case "$arg" in
    --skip-login) SKIP_LOGIN=1 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

step() { printf '==> %s\n' "$1"; }
fail() { printf 'error: %s\n' "$1" >&2; exit 1; }

step "Checking the platform at $PLATFORM_URL"
curl -fsS --max-time 10 "$DOWNLOADS/version.json" >/dev/null \
  || fail "can't reach the platform at $DOWNLOADS — check the address and that you're on the company network"

step "Finding Python 3.11 or newer"
PYTHON=""
for candidate in python3 python; do
  if command -v "$candidate" >/dev/null 2>&1 \
     && "$candidate" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' 2>/dev/null; then
    PYTHON="$candidate"; break
  fi
done
[ -n "$PYTHON" ] || fail "Python 3.11 or newer is required"

step "Installing the Company Deployment MCP server into $VENV"
mkdir -p "$ROOT"
chmod 700 "$ROOT"
[ -x "$VENV/bin/python" ] || "$PYTHON" -m venv "$VENV" || fail "creating the virtual environment failed (on Debian/Ubuntu: apt install python3-venv)"
curl -fsSL "$DOWNLOADS/$WHEEL" -o "$ROOT/$WHEEL"
"$VENV/bin/python" -m pip install --quiet --disable-pip-version-check --upgrade "$ROOT/$WHEEL" \
  || fail "installing the MCP server failed (its dependencies come from PyPI or your pip mirror)"
# The wheel's version doesn't change between platform updates, so on a
# re-run pip calls it "already installed" and keeps the old code. Replace
# the package itself every time; its dependencies stay as installed above.
"$VENV/bin/python" -m pip install --quiet --disable-pip-version-check --force-reinstall --no-deps "$ROOT/$WHEEL" \
  || fail "updating the MCP server failed"
rm -f "$ROOT/$WHEEL"

step "Installing the Company Deployment skill into $SKILL_DIR"
curl -fsSL "$DOWNLOADS/company-deployment-skill.zip" -o "$ROOT/skill.zip"
rm -rf "$SKILL_DIR"
mkdir -p "$SKILL_DIR"
"$VENV/bin/python" -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$ROOT/skill.zip" "$SKILL_DIR"
rm -f "$ROOT/skill.zip"

step "Trusting the platform's HTTPS certificate"
# The company CA that signed it (DEC-031). The MCP server and company-deploy
# trust it alongside the public authorities; nothing else on this machine
# changes. A platform without HTTPS has none, which is fine.
CA_FILE="$ROOT/company-platform-ca.crt"
if curl -fsS "$DOWNLOADS/company-platform-ca.crt" -o "$CA_FILE.tmp" 2>/dev/null; then
  mv -f "$CA_FILE.tmp" "$CA_FILE"
  "$VENV/bin/python" -c 'import hashlib, ssl, sys; d = ssl.PEM_cert_to_DER_cert(open(sys.argv[1]).read()); h = hashlib.sha256(d).hexdigest().upper(); print("    company CA, SHA-256 " + ":".join(h[i:i+2] for i in range(0, len(h), 2)))' "$CA_FILE"
else
  rm -f "$CA_FILE.tmp"
  case "$PLATFORM_URL" in https://*) fail "couldn't fetch the company CA from $DOWNLOADS" ;; esac
  echo "    none published; the platform is HTTP only"
fi

step "Registering the MCP server with Claude Code"
if command -v claude >/dev/null 2>&1; then
  claude mcp remove company-deployment -s user >/dev/null 2>&1 || true
  claude mcp add company-deployment -s user -e "PLATFORM_API_BASE_URL=$API" -e "COMPANY_DOWNLOADS_URL=$DOWNLOADS" -- "$VENV/bin/python" -m mcp_server.server
else
  echo "warning: Claude Code (the 'claude' command) isn't installed or not on PATH. Once it is, run:" >&2
  echo "  claude mcp add company-deployment -s user -e PLATFORM_API_BASE_URL=$API -e COMPANY_DOWNLOADS_URL=$DOWNLOADS -- \"$VENV/bin/python\" -m mcp_server.server" >&2
fi

if [ "$SKIP_LOGIN" = 1 ]; then
  step "Skipping sign-in. Later, run: $VENV/bin/company-deploy login --url $API"
else
  step "Signing this machine in"
  "$VENV/bin/company-deploy" login --url "$API" \
    || fail "sign-in didn't complete; run it again with: $VENV/bin/company-deploy login --url $API"
fi

echo
echo "Done. Open Claude Code in your project and ask it to deploy — it uses the company-deployment skill."
echo "Admin Portal: $PLATFORM_URL"
