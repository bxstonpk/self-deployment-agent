#!/bin/sh
# Seals the Company Platform's secret key (PLATFORM_SECRET_KEY) with this
# machine's TPM 2.0 (DEC-006, decided 2026-10-23). Afterwards the key exists
# on disk only as a blob this machine's TPM alone can open; it is unsealed
# into RAM at boot by tpm-unseal-key.sh.
#
#   sudo ./tpm-seal-key.sh --from-env /srv/Self-Deployment_Agent/.env
#   sudo ./tpm-seal-key.sh --generate           # a new platform: a fresh key
#
# Options:
#   --out DIR     where the sealed blob goes (default /etc/company-platform)
#   --pcrs LIST   also bind to these PCRs, e.g. 7 (Secure Boot state). The
#                 key then won't unseal after a firmware or Secure Boot
#                 change until resealed — stronger, but plan for it.
#
# It also writes an ESCROW copy, encrypted with a passphrase you choose
# (ESCROW_PASSPHRASE, or asked for): the only way back to the key if this
# machine's TPM or mainboard dies. Move it off the machine and keep it
# offline. Without the key, every stored secret and every backup is
# unreadable.
#
# Needs tpm2-tools and openssl. Uses /dev/tpmrm0 unless TPM2TOOLS_TCTI says
# otherwise (the verification script points it at a software TPM).
set -eu

OUT=/etc/company-platform
PCRS=""
SOURCE=""
ENV_FILE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --from-env) SOURCE=env; ENV_FILE="$2"; shift 2 ;;
    --generate) SOURCE=generate; shift ;;
    --out) OUT="$2"; shift 2 ;;
    --pcrs) PCRS="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
fail() { echo "error: $*" >&2; exit 1; }
[ -n "$SOURCE" ] || fail "give --from-env <.env file> or --generate"
for tool in tpm2_createprimary tpm2_create tpm2_load tpm2_unseal openssl base64; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is missing (apt install tpm2-tools openssl)"
done
[ -e "$OUT/seal.pub" ] && fail "$OUT already holds a sealed key; move it away first"

umask 077
WORK=$(mktemp -d "${TMPDIR:-/dev/shm}/seal.XXXXXX" 2>/dev/null || mktemp -d)
trap 'rm -rf "$WORK"' EXIT

if [ "$SOURCE" = env ]; then
  KEY=$(sed -n 's/^PLATFORM_SECRET_KEY=//p' "$ENV_FILE" | tail -n 1 | tr -d '\r')
  [ -n "$KEY" ] || fail "no PLATFORM_SECRET_KEY in $ENV_FILE"
else
  KEY=$(head -c 32 /dev/urandom | base64 | tr -d '\n')
fi
[ "$(printf '%s' "$KEY" | base64 -d 2>/dev/null | wc -c)" -eq 32 ] || fail "the key must be 32 bytes, base64-encoded"
printf '%s' "$KEY" > "$WORK/key"

# Transient objects are flushed after every step: a kernel resource manager
# (/dev/tpmrm0) does this by itself, a bare TPM connection (a software TPM,
# or an old kernel) does not, and runs out of object slots.
flush() { tpm2_flushcontext -t 2>/dev/null || true; }
flush
tpm2_createprimary -Q -C o -g sha256 -G ecc -c "$WORK/primary.ctx"
flush
if [ -n "$PCRS" ]; then
  tpm2_startauthsession -Q -S "$WORK/session.ctx"
  tpm2_policypcr -Q -S "$WORK/session.ctx" -l "sha256:$PCRS" -L "$WORK/pcr.policy"
  tpm2_flushcontext "$WORK/session.ctx"
  tpm2_create -Q -C "$WORK/primary.ctx" -L "$WORK/pcr.policy" -a "fixedtpm|fixedparent" \
    -i "$WORK/key" -u "$WORK/seal.pub" -r "$WORK/seal.priv"
else
  tpm2_create -Q -C "$WORK/primary.ctx" -i "$WORK/key" -u "$WORK/seal.pub" -r "$WORK/seal.priv"
fi

flush

mkdir -p "$OUT"
chmod 700 "$OUT"
install -m 600 "$WORK/seal.pub" "$WORK/seal.priv" "$OUT/"
printf '%s\n' "$PCRS" > "$OUT/pcrs"
chmod 600 "$OUT/pcrs"

# Prove it unseals before anyone relies on it.
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
"$SCRIPT_DIR/tpm-unseal-key.sh" --in "$OUT" --out "$WORK/check"
cmp -s "$WORK/key" "$WORK/check" || fail "the sealed key did not unseal to the same value; nothing should use $OUT"

if [ -z "${ESCROW_PASSPHRASE:-}" ]; then
  stty -echo 2>/dev/null || true
  printf 'Escrow passphrase (keep it with the escrow file, offline): ' >&2
  read -r ESCROW_PASSPHRASE
  printf '\nAgain: ' >&2
  read -r again
  stty echo 2>/dev/null || true
  echo >&2
  [ "$ESCROW_PASSPHRASE" = "$again" ] || fail "the passphrases didn't match"
fi
[ "${#ESCROW_PASSPHRASE}" -ge 12 ] || fail "use an escrow passphrase of at least 12 characters"
export ESCROW_PASSPHRASE
openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt -in "$WORK/key" -out "$OUT/secret-key.escrow" -pass env:ESCROW_PASSPHRASE
chmod 600 "$OUT/secret-key.escrow"

echo "Sealed with this machine's TPM in $OUT${PCRS:+ (bound to PCRs $PCRS)}."
echo "Next:"
echo "  1. copy $OUT/secret-key.escrow off this machine and keep it offline, with its passphrase"
echo "  2. install tpm-unseal-key.sh and company-platform-unseal.service (see README.md) and start it"
echo "  3. remove PLATFORM_SECRET_KEY from .env and start the platform with deploy/host/docker-compose.tpm.yml"
