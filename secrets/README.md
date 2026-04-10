# secrets/

Bind-mounted into `brainstorm-server` at `/run/secrets` (read-only) and into
`nsec-rotate` at `/run/secrets` (read-write).

Gitignored — never commit files in this directory.

## nsec_encryption_keys

Comma-separated Fernet keys, newest first. Empty file or missing file = no encryption.

### Bootstrap

```bash
mkdir -p secrets
docker compose up -d brainstorm-server
docker compose --profile ops run --rm nsec-rotate
```

The rotation script detects the missing key file, generates one inside the container, writes it to `./secrets/nsec_encryption_keys` via the bind mount, and encrypts existing rows.

### Rotate

```bash
docker compose --profile ops run --rm nsec-rotate
```

Zero downtime. See `brainstorm_server/README.md` for details.

## Backup and migration

The key file and the `brainstorm_nsec` table are **paired**. Back them up together, restore them together, migrate them together. Losing one without the other means either unreadable data (no key) or a useless key (no data).

Backup:

```bash
cat secrets/nsec_encryption_keys   # copy into password manager / vault
```

Restore:

```bash
printf '%s' "<backed-up-key>" > secrets/nsec_encryption_keys
chmod 600 secrets/nsec_encryption_keys
docker compose up -d brainstorm-server
```
