# secrets/

Bind-mounted **read-write** into `brainstorm-server` at `/run/secrets`. The app
needs write access so the in-process key rotation flow can update
`nsec_encryption_keys` atomically.

Gitignored — never commit files in this directory.

## nsec_encryption_keys

Comma-separated Fernet keys, newest first.

### Bootstrap

No manual bootstrap step. On first startup, if the file is missing or empty, the
app auto-generates a fresh Fernet key, writes it here via the rw bind mount, and
encrypts any pre-existing plaintext rows.

**After first bootstrap, copy this file to your secrets vault.**

### Rotate

Triggered via the admin endpoint — no ops container:

```
POST /admin/nsec-encryption/rotate
POST /admin/nsec-encryption/verify
```

Caller must be in `admin_whitelisted_pubkeys`. See `brainstorm_server/README.md`
for the full flow.

## Backup and migration

The key file and the `brainstorm_nsec` table are **paired**. Back them up
together, restore them together, migrate them together. Losing one without the
other means either unreadable data (no key) or a useless key (no data).

Back up the file to your secrets vault after every rotation:

```bash
cat secrets/nsec_encryption_keys   # copy into password manager / vault
```

Restore:

```bash
printf '%s' "<backed-up-key>" > secrets/nsec_encryption_keys
chmod 600 secrets/nsec_encryption_keys
docker compose up -d brainstorm-server
```
