# Backup Fleet Monitor

**English** | [Bahasa Indonesia](README.id.md)

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

- **TLS is terminated at your edge** (F5, FortiWeb, Cloudflare...). The containers
  themselves speak plain HTTP and do not need certificates. Keep `COOKIE_SECURE=true`
  (the default): the browser sees HTTPS, so the `Secure` cookie works. Use `false`
  only for direct local HTTP testing. Set HSTS at the edge.
- **Real client IP.** Behind a proxy all requests come from the proxy's address;
  configure `set_real_ip_from` in `web/nginx.conf` so login rate limiting and audit
  logs use the real client IP.
- **Back up `SECRET_KEY`.** Source credentials are stored encrypted with it
  (AES-256-GCM); losing it means re-entering every credential.
- Never commit `.env`. It is git-ignored.
- Only the `web` container is published; `api` and `postgres` stay on the
  internal Docker network. Do not expose the API port directly: the app trusts
  the `X-Real-IP` header set by the bundled nginx.
- Users with `sources.create`/`sources.update` can make the server connect to
  any HTTPS host they enter (needed to poll your backup systems). Users with `alerts.configure`
  likewise choose where notifications are sent (SMTP host, webhook URL). Grant those
  permissions only to trusted administrators and restrict egress at the network
  level if needed.
- Node aliases (IP/GUID to FQDN) are stored in the `node_aliases` table; seed
  your own with SQL, see `backend/cmd/server/migrations/008_node_aliases.sql`.

## Alerts and notifications

The poller checks the backup state every 5 minutes and raises an alert for:
failed backups, stale backups (no success for N hours), storage pools above a
threshold (>= 95% is critical) and unreachable sources. An alert is notified once,
re-sent as a reminder every `repeatHours` while unacknowledged, and a recovery
notice is sent when it resolves. Acknowledging an alert silences its reminders.

Configure **Alerts -> Notification channels** (permission `alerts.configure`):
webhook (Teams, Slack, any JSON receiver), Telegram bot, or email (SMTP, STARTTLS/TLS).
Credentials and URLs are stored encrypted and never shown again. Webhook URLs must be
https (set `NOTIFY_ALLOW_HTTP=true` to allow plain http to an internal receiver).
If every channel fails, the alert stays pending and is retried on the next cycle.

## User management

Super admins can create users, change names and roles, reset passwords (a one-time
temporary password; the user must change it at next sign-in), revoke sessions,
disable/re-enable and delete disabled users. Safeguards: you cannot change your own
role or act on your own account, the last active super admin is protected, and only a
super admin may touch a super admin.

## Data retention

**Settings -> Data retention** sets how long audit logs, backup session history,
Synology ABB events, poll history and resolved alerts are kept (0 = forever; sensible
minimums are enforced). The purge runs once a day, can be previewed and run manually,
and is itself written to the audit log. The audit log stays append-only: deleting old
rows is only possible inside the purge transaction.

Report vulnerabilities privately to the maintainer rather than in public issues.

## License

MIT, see [LICENSE](LICENSE).
