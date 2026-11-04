# Security Policy

## Reporting a vulnerability

Please do **not** open a public issue for security problems.

Use GitHub's private reporting: go to the **Security** tab of this repository and
choose **Report a vulnerability**. Include the affected version/commit, steps to
reproduce, and the impact you observed.

You can expect an acknowledgement within a few days. Fixes are published on `main`;
please allow reasonable time to fix before any public disclosure.

## Scope

In scope: the API (`backend/`), the web UI and nginx config (`web/`), and the
Docker Compose setup.

Out of scope: vulnerabilities in the backup platforms this tool reads from
(Proxmox VE, Veeam, Synology) and in upstream dependencies without a
demonstrable impact on this project.

## Deployment guidance

- Terminate TLS at your edge (load balancer / WAF / CDN) and keep
  `COOKIE_SECURE=true`.
- Never commit `.env`; back up `SECRET_KEY` (it encrypts stored credentials).
- Publish only the `web` container; keep `api` and `postgres` on the internal
  network.
- Grant `sources.create` / `sources.update` only to trusted administrators: those
  users choose which HTTPS hosts the server connects to.
