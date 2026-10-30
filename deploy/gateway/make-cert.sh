#!/bin/sh
# Makes the gateway's self-signed HTTPS certificate: a company CA (made once,
# kept) and a server certificate it signs for the platform's addresses.
# Run on the platform host, from the repository root:
#
#   deploy/gateway/make-cert.sh 192.168.10.25 [more IPs or host names...]
#   docker compose up -d gateway
#
# Writes:
#   tls/ca.crt, tls/server.crt, tls/server.key   mounted into the gateway
#   tls-ca/ca.key                                the CA's private key: never
#                                                mounted, never served; keep it
#                                                to issue the next certificate
#
# Run it again (e.g. before the certificate expires, or for a new address)
# and it issues a new server certificate from the same CA, so machines that
# already trust the CA need nothing new. --new-ca replaces the CA too, and
# every machine must then trust the new one.
set -eu

TLS_DIR="${TLS_DIR:-tls}"
CA_DIR="${CA_DIR:-tls-ca}"
CA_DAYS=3650
SERVER_DAYS=825

new_ca=0
names=""
for arg in "$@"; do
  case "$arg" in
    --new-ca) new_ca=1 ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) names="$names $arg" ;;
  esac
done
[ -n "$names" ] || { echo "usage: $0 [--new-ca] <ip-or-name> [...]" >&2; exit 2; }
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }

umask 077
mkdir -p "$TLS_DIR" "$CA_DIR"

if [ "$new_ca" = 1 ] || [ ! -s "$CA_DIR/ca.key" ] || [ ! -s "$TLS_DIR/ca.crt" ]; then
  openssl req -x509 -new -newkey rsa:4096 -nodes -sha256 -days "$CA_DAYS" \
    -subj "/O=Company Deployment Platform/CN=Company Deployment Platform CA" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -keyout "$CA_DIR/ca.key" -out "$TLS_DIR/ca.crt" 2>/dev/null
  echo "made a new company CA"
fi

san=""
first=""
for n in $names; do
  case "$n" in
    *[!0-9.]*) entry="DNS:$n" ;;
    *) entry="IP:$n" ;;
  esac
  san="${san:+$san,}$entry"
  first="${first:-$n}"
done

ext=$(mktemp)
trap 'rm -f "$ext" "$TLS_DIR/server.csr"' EXIT
printf 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=%s\n' "$san" > "$ext"

openssl req -new -newkey rsa:2048 -nodes -sha256 \
  -subj "/O=Company Deployment Platform/CN=$first" \
  -keyout "$TLS_DIR/server.key" -out "$TLS_DIR/server.csr" 2>/dev/null
openssl x509 -req -sha256 -days "$SERVER_DAYS" -in "$TLS_DIR/server.csr" \
  -CA "$TLS_DIR/ca.crt" -CAkey "$CA_DIR/ca.key" -CAcreateserial -CAserial "$CA_DIR/ca.srl" \
  -extfile "$ext" -out "$TLS_DIR/server.crt" 2>/dev/null
rm -f "$TLS_DIR/server.csr"

chmod 644 "$TLS_DIR/ca.crt" "$TLS_DIR/server.crt"
chmod 600 "$TLS_DIR/server.key" "$CA_DIR/ca.key"
chmod 755 "$TLS_DIR"

openssl verify -CAfile "$TLS_DIR/ca.crt" "$TLS_DIR/server.crt" >/dev/null
echo "server certificate for: $san"
openssl x509 -in "$TLS_DIR/server.crt" -noout -enddate
printf 'CA fingerprint (check it when trusting ca.crt): '
openssl x509 -in "$TLS_DIR/ca.crt" -noout -fingerprint -sha256 | cut -d= -f2
echo "next: docker compose up -d gateway"
