#!/bin/sh
# Sets up HTTP and HTTPS before nginx starts.
#  - With a certificate (deploy/gateway/make-cert.sh), HTTPS is on.
#  - GATEWAY_HTTP_MODE=redirect closes HTTP: every address moves to HTTPS,
#    except the installers and the company CA (http-redirect.conf.template).
#    The default, serve, keeps HTTP serving everything.
# Without a certificate the gateway serves HTTP only, as before.
set -eu

TLS=/etc/nginx/tls
MODE="${GATEWAY_HTTP_MODE:-serve}"

# HTTP serves everything unless it is closed below.
cp /etc/nginx/platform-http.conf /etc/nginx/conf.d/default.conf

if [ -s "$TLS/server.crt" ] && [ -s "$TLS/server.key" ]; then
  cp /etc/nginx/https.conf.template /etc/nginx/conf.d/https.conf
  echo "platform https: on"
  if [ "$MODE" = redirect ]; then
    # The HTTPS address: PLATFORM_PUBLIC_BASE_URL when it is one, otherwise
    # this host on 443.
    case "${PLATFORM_PUBLIC_BASE_URL:-}" in
      https://*) https_url="${PLATFORM_PUBLIC_BASE_URL%/}" ;;
      *)         https_url='https://$host' ;;
    esac
    sed "s|__HTTPS_URL__|$https_url|g" /etc/nginx/http-redirect.conf.template > /etc/nginx/conf.d/default.conf
    echo "platform http: closed, redirects to $https_url (the installers and the CA stay on HTTP)"
  fi
else
  rm -f /etc/nginx/conf.d/https.conf
  echo "platform https: off (no certificate in $TLS)"
  if [ "$MODE" = redirect ]; then
    echo "platform http: GATEWAY_HTTP_MODE=redirect ignored: without a certificate HTTP must stay open"
  fi
fi
