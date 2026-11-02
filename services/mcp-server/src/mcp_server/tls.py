"""Which certificates this machine trusts for the Company Platform.

The platform serves HTTPS with a certificate signed by the company's own CA
(DEC-031), which no public trust store knows. The installer puts that CA
next to the machine's key, at ~/.company-deploy/company-platform-ca.crt
(override with COMPANY_PLATFORM_CA), and every request to the platform
trusts it as well as the public authorities (certifi). Without the file,
only the public authorities are trusted, as before.
"""

from __future__ import annotations

import os
import ssl
from pathlib import Path

import certifi

CA_FILENAME = "company-platform-ca.crt"


def ca_path() -> Path:
    override = os.environ.get("COMPANY_PLATFORM_CA")
    if override:
        return Path(override).expanduser()
    return Path.home() / ".company-deploy" / CA_FILENAME


def ssl_context() -> ssl.SSLContext:
    ctx = ssl.create_default_context(cafile=certifi.where())
    ca = ca_path()
    if ca.is_file():
        ctx.load_verify_locations(cafile=str(ca))
    return ctx
