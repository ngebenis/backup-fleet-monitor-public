# Backup Fleet Monitor

Read-only dashboard that aggregates backup status from Proxmox VE, Veeam
(Enterprise Manager REST or a push collector) and Synology Active Backup for
Business. Go API + poller, PostgreSQL, React/Vite UI behind nginx.

## Quick start

```bash
cp .env.example .env
chmod 600 .env
# fill in POSTGRES_PASSWORD, ADMIN_BOOTSTRAP_PASSWORD (>=12 chars) and:
openssl rand -hex 32   # -> SECRET_KEY
docker compose up -d --build
```

Open `http://<host>:8081` and sign in with the bootstrap admin. You are forced
to change the password on first login.

## Security notes

- **Run behind HTTPS.** `COOKIE_SECURE` defaults to `true`; the login cookie is
  then only sent over TLS. Use `COOKIE_SECURE=false` solely for local HTTP
  testing. Terminate TLS in a reverse proxy/load balancer and enable the HSTS
  line in `web/nginx.conf`.
- **Back up `SECRET_KEY`.** Source credentials are stored encrypted with it
  (AES-256-GCM); losing it means re-entering every credential.
- Never commit `.env`. It is git-ignored.
- Only the `web` container is published; `api` and `postgres` stay on the
  internal Docker network. Do not expose the API port directly: the app trusts
  the `X-Real-IP` header set by the bundled nginx.
- Users with `sources.create`/`sources.update` can make the server connect to
  any HTTPS host they enter (needed to poll your backup systems). Grant those
  permissions only to trusted administrators and restrict egress at the network
  level if needed.
- Node aliases (IP/GUID to FQDN) are stored in the `node_aliases` table; seed
  your own with SQL, see `backend/cmd/server/migrations/008_node_aliases.sql`.

Report vulnerabilities privately to the maintainer rather than in public issues.

## License

MIT, see [LICENSE](LICENSE).
