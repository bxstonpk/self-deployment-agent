#!/bin/sh
# Verifies tpm-seal-key.sh / tpm-unseal-key.sh against a software TPM
# (swtpm) — the real tpm2-tools commands, a real TPM 2.0 implementation,
# just not a chip. Run in a throwaway container:
#
#   docker run --rm -v "$PWD/deploy/host:/host:ro" -v "<dir>:/out" debian:bookworm-slim sh /host/verify-tpm.sh
#
# With KEY_TO_SEAL set, it seals that key (and leaves the unsealed copy in
# /out/secret-key for the platform check); otherwise a generated one.
set -eu
apt-get update -qq >/dev/null && apt-get install -y -qq swtpm swtpm-tools tpm2-tools openssl >/dev/null 2>&1

ok() { echo "OK:   $*"; }
bad() { echo "FAIL: $*"; FAILED=1; }
FAILED=0
export TPM2TOOLS_TCTI="swtpm:host=127.0.0.1,port=2321"
start_tpm() { # $1 = state dir
  mkdir -p "$1"
  swtpm socket --tpmstate dir="$1" --tpm2 --server type=tcp,port=2321 --ctrl type=tcp,port=2322 \
    --flags not-need-init,startup-clear --daemon --pid file="$1/pid"
  sleep 1
}
stop_tpm() { kill "$(cat "$1/pid")" 2>/dev/null || true; sleep 1; }

start_tpm /tpm/machine-a
if [ -n "${KEY_TO_SEAL:-}" ]; then
  printf 'PLATFORM_SECRET_KEY=%s\n' "$KEY_TO_SEAL" > /tmp/.env
  SOURCE="--from-env /tmp/.env"
else
  SOURCE="--generate"
fi
export ESCROW_PASSPHRASE="escrow passphrase for verification"

# shellcheck disable=SC2086
if sh /host/tpm-seal-key.sh $SOURCE --out /etc/company-platform >/tmp/seal.log 2>&1; then
  ok "tpm-seal-key.sh sealed the key (and proved it unseals before finishing)"
else
  bad "sealing failed: $(cat /tmp/seal.log)"; exit 1
fi
ls -l /etc/company-platform | sed 's/^/      /'
grep -q "PLATFORM_SECRET_KEY\|$(printf '%s' "${KEY_TO_SEAL:-none}" | cut -c1-12)" /etc/company-platform/seal.priv \
  && bad "the key is readable in the sealed blob" || ok "the sealed blob doesn't contain the key"

sh /host/tpm-unseal-key.sh --out /run/company-platform/secret-key
KEY=$(cat /run/company-platform/secret-key)
[ "$(printf '%s' "$KEY" | base64 -d | wc -c)" -eq 32 ] && ok "unsealed on the same TPM: a 32-byte key, file mode $(stat -c %a /run/company-platform/secret-key)" || bad "unsealed key is wrong"
[ -z "${KEY_TO_SEAL:-}" ] || { [ "$KEY" = "$KEY_TO_SEAL" ] && ok "it is exactly the key that was in .env" || bad "unsealed a different key"; }

stop_tpm /tpm/machine-a
start_tpm /tpm/machine-a
rm -f /run/company-platform/secret-key
sh /host/tpm-unseal-key.sh && [ "$(cat /run/company-platform/secret-key)" = "$KEY" ] \
  && ok "after the TPM restarts (a reboot), it unseals again" || bad "no unseal after restart"

stop_tpm /tpm/machine-a
start_tpm /tpm/machine-b
if sh /host/tpm-unseal-key.sh --out /tmp/stolen 2>/tmp/stolen.log; then
  bad "another machine's TPM unsealed the key"
else
  ok "copied to another machine's TPM, the blob doesn't unseal: $(tail -n 1 /tmp/stolen.log)"
fi
stop_tpm /tpm/machine-b

RECOVERED=$(ESCROW_PASSPHRASE="escrow passphrase for verification" openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 \
  -in /etc/company-platform/secret-key.escrow -pass env:ESCROW_PASSPHRASE)
[ "$RECOVERED" = "$KEY" ] && ok "the escrow copy recovers the key with its passphrase" || bad "escrow recovery failed"
if ESCROW_PASSPHRASE="wrong" openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -in /etc/company-platform/secret-key.escrow \
     -pass env:ESCROW_PASSPHRASE >/tmp/wrong 2>/dev/null && [ "$(cat /tmp/wrong)" = "$KEY" ]; then
  bad "a wrong escrow passphrase recovered the key"
else
  ok "a wrong escrow passphrase doesn't"
fi

# PCR binding: a Secure Boot or firmware change moves PCR 7.
start_tpm /tpm/machine-c
sh /host/tpm-seal-key.sh --generate --pcrs 7 --out /etc/pcr-bound >/tmp/pcr.log 2>&1 && ok "sealed a key bound to PCR 7" || { bad "PCR seal failed: $(cat /tmp/pcr.log)"; }
sh /host/tpm-unseal-key.sh --in /etc/pcr-bound --out /tmp/pcr-key && ok "with PCR 7 unchanged it unseals" || bad "PCR-bound unseal failed"
tpm2_pcrextend 7:sha256=0000000000000000000000000000000000000000000000000000000000000001
if sh /host/tpm-unseal-key.sh --in /etc/pcr-bound --out /tmp/pcr-key2 2>/tmp/pcr2.log; then
  bad "unsealed after PCR 7 changed"
else
  ok "after PCR 7 changes (Secure Boot/firmware), it refuses: $(tail -n 1 /tmp/pcr2.log)"
fi
stop_tpm /tpm/machine-c

if [ -d /out ]; then
  cp /run/company-platform/secret-key /out/secret-key 2>/dev/null || true
fi
[ "$FAILED" -eq 0 ] && echo "All checks passed." || { echo "Some checks FAILED."; exit 1; }
