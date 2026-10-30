#!/bin/sh
# Turns HTTPS on when a certificate is installed (deploy/gateway/make-cert.sh),
# before nginx starts. Without one the gateway serves HTTP only, as before.
set -eu

TLS=/etc/nginx/tls
CONF=/etc/nginx/conf.d/https.conf

if [ -s "$TLS/server.crt" ] && [ -s "$TLS/server.key" ]; then
  # What the installers get when downloaded over HTTPS: the platform's HTTP
  # address. PLATFORM_PUBLIC_BASE_URL when it is an http:// address,
  # otherwise this host on port 80.
  case "${PLATFORM_PUBLIC_BASE_URL:-}" in
    http://*) install_url="${PLATFORM_PUBLIC_BASE_URL%/}" ;;
    *)        install_url='http://$host' ;;
  esac
  sed "s|__INSTALL_URL__|$install_url|" /etc/nginx/https.conf.template > "$CONF"
  echo "platform https: on"
else
  rm -f "$CONF"
  echo "platform https: off (no certificate in $TLS)"
fi
