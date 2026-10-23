#!/bin/sh
# Unseals the Company Platform's secret key with this machine's TPM into
# RAM (/run is a tmpfs), where platform-api reads it (DEC-006). Run at boot
# by company-platform-unseal.service; safe to run again.
#
#   tpm-unseal-key.sh [--in /etc/company-platform] [--out /run/company-platform/secret-key]
#
# On another machine's TPM — or this one after a PCR-bound seal's PCRs
# changed — this fails, and the platform does not start: recover with the
# escrow copy (README.md).
set -eu

IN=/etc/company-platform
OUT=/run/company-platform/secret-key
while [ $# -gt 0 ]; do
  case "$1" in
    --in) IN="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
fail() { echo "error: $*" >&2; exit 1; }
[ -r "$IN/seal.pub" ] && [ -r "$IN/seal.priv" ] || fail "no sealed key in $IN (run tpm-seal-key.sh first)"

umask 077
WORK=$(mktemp -d "${TMPDIR:-/dev/shm}/unseal.XXXXXX" 2>/dev/null || mktemp -d)
trap 'rm -rf "$WORK"' EXIT
PCRS=$(cat "$IN/pcrs" 2>/dev/null || true)

# The same primary key every time: derived from this TPM's storage seed,
# which is what ties the blob to this machine.
# Flushed after every step: see tpm-seal-key.sh.
flush() { tpm2_flushcontext -t 2>/dev/null || true; }
trap 'flush; rm -rf "$WORK"' EXIT
flush
tpm2_createprimary -Q -C o -g sha256 -G ecc -c "$WORK/primary.ctx"
flush
tpm2_load -Q -C "$WORK/primary.ctx" -u "$IN/seal.pub" -r "$IN/seal.priv" -c "$WORK/seal.ctx" \
  || fail "this TPM can't load the sealed key — it was sealed on another machine, or this TPM was cleared"
flush
mkdir -p "$(dirname "$OUT")"
if [ -n "$PCRS" ]; then
  tpm2_startauthsession -Q --policy-session -S "$WORK/session.ctx"
  tpm2_policypcr -Q -S "$WORK/session.ctx" -l "sha256:$PCRS"
  tpm2_unseal -c "$WORK/seal.ctx" -p "session:$WORK/session.ctx" -o "$OUT.tmp" \
    || { tpm2_flushcontext "$WORK/session.ctx" 2>/dev/null; fail "PCRs $PCRS no longer match what the key was sealed to (firmware or Secure Boot changed?)"; }
  tpm2_flushcontext "$WORK/session.ctx"
else
  tpm2_unseal -c "$WORK/seal.ctx" -o "$OUT.tmp" || fail "unsealing failed"
fi
chmod 400 "$OUT.tmp"
mv -f "$OUT.tmp" "$OUT"
