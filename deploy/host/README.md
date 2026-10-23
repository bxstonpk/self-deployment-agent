# Platform host: the secret key sealed with the TPM (DEC-006)

`PLATFORM_SECRET_KEY` encrypts every application secret, every generated
database password and every database backup. The platform owner decided
on 2026-10-23 (`DEC-006`, `docs/17_Decision_Log.md`) to **bind it to the
host's TPM**:

- **On disk:** the key exists only as a blob that this machine's TPM 2.0
  alone can open.
- **At boot:** it is unsealed into RAM (`/run`, a tmpfs) and nowhere else.
- **In the container:** platform-api reads it from that file. It is no
  longer in `.env`, and no longer in the container's environment, where
  `docker inspect` showed it.
- **Escrow:** a passphrase-encrypted copy, kept offline, is the way back
  if the machine's TPM or mainboard dies.

| File | What it is |
|---|---|
| `tpm-seal-key.sh` | Seals the key (from `.env`, or a new one) and writes the escrow copy. Run once. |
| `tpm-unseal-key.sh` | Unseals into `/run/company-platform/secret-key`. Run at boot. |
| `company-platform-unseal.service` | The systemd unit that runs it at boot, before Docker. |
| `docker-compose.tpm.yml` | Compose override: platform-api reads the unsealed file instead of `.env`. |
| `verify-tpm.sh` | The verification, run against a software TPM (below). |

## Setting it up (IT, on the platform host)

Requirements:
- the host has a TPM 2.0 (`ls /dev/tpmrm0`), enabled in the firmware;
- `sudo apt install tpm2-tools openssl`.

```bash
cd /srv/Self-Deployment_Agent            # wherever the repository is

# 1. Seal the key the platform already uses (or --generate on a new platform).
sudo deploy/host/tpm-seal-key.sh --from-env .env
#    asks for an escrow passphrase; writes /etc/company-platform/{seal.pub,seal.priv,secret-key.escrow}

# 2. Move the escrow copy off the machine and keep it offline, with its passphrase.
sudo cp /etc/company-platform/secret-key.escrow /media/usb/ && sudo shred -u /etc/company-platform/secret-key.escrow

# 3. Unseal at every boot.
sudo install -d /opt/company-platform
sudo install -m 755 deploy/host/tpm-unseal-key.sh /opt/company-platform/
sudo install -m 644 deploy/host/company-platform-unseal.service /etc/systemd/system/
sudo systemctl enable --now company-platform-unseal

# 4. Take the key out of .env and start the platform reading the file.
sed -i '/^PLATFORM_SECRET_KEY=/d' .env
docker compose -f docker-compose.yml -f deploy/host/docker-compose.tpm.yml up -d
docker compose logs platform-api | grep "sealing with key"   # the same key id as before
```

Always start the platform with both compose files from then on.

**Binding to PCRs as well** (`--pcrs 7`) ties the key to the machine's
Secure Boot state too, not just its TPM. A firmware update or a Secure
Boot change then stops the key from unsealing until it is resealed: the
platform won't start, and the escrow copy is how you get back. Use it only
with a plan for firmware updates. Without it, the key is bound to this
TPM alone.

## When it won't unseal

- **TPM cleared, or the mainboard replaced:** the blob can't be loaded.
- **PCR-bound and the PCRs changed:** the key is refused.

Either way platform-api refuses to start, rather than starting without its
key. Recover from the escrow copy:

```bash
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -in secret-key.escrow > /dev/shm/key   # asks for the passphrase
printf 'PLATFORM_SECRET_KEY=%s\n' "$(cat /dev/shm/key)" > /dev/shm/env
sudo mv /etc/company-platform /etc/company-platform.old
sudo deploy/host/tpm-seal-key.sh --from-env /dev/shm/env    # seal it to this TPM; new escrow copy
shred -u /dev/shm/key /dev/shm/env
sudo systemctl restart company-platform-unseal
```

## How it was verified

`verify-tpm.sh` runs the real scripts against **swtpm**, a complete
software TPM 2.0, in a throwaway Debian container. It sealed the local
platform's actual key. Results:

- **Sealing:** sealing proved itself by unsealing before finishing, and
  the blob doesn't contain the key.
- **Unsealing:** the same TPM unsealed exactly the key from `.env` (a
  32-byte key, file mode 400), and did so again after the TPM restarted,
  as after a reboot.
- **Binding:** copied to another TPM, the blob didn't unseal ("sealed on
  another machine").
- **Escrow:** the copy recovered the key with its passphrase, and not with
  a wrong one.
- **PCR binding:** a key bound to PCR 7 unsealed until PCR 7 was extended,
  as a Secure Boot change would, and was then refused.

Then platform-api was started with `docker-compose.tpm.yml`, reading that
unsealed key from a file:
- it logged the **same key id** as before (`sealing with key 175d88b7…`);
- its environment no longer held the key;
- a secret set through the API was decrypted into a newly deployed
  application's container.

```bash
docker run --rm -v "$PWD/deploy/host:/host:ro" debian:bookworm-slim sh /host/verify-tpm.sh
```

Not verified on a physical TPM chip. The tpm2-tools commands are the same,
and on a real host `/dev/tpmrm0`'s resource manager does the flushing the
scripts also do themselves for a software TPM.

## What this does and doesn't protect against

- **Protects:** a copy of the disk, the backups or the platform database
  (including a stolen drive) yields no key and so no secrets. Neither do
  `.env` or `docker inspect`.
- **Doesn't protect:** someone with root on the running host. They can
  read `/run/company-platform/secret-key` or platform-api's memory, as
  with any key a running service uses.
- **Not provided:** rotating the key itself. There is no re-encryption
  tool, so changing the key still makes existing secrets and backups
  unreadable.
