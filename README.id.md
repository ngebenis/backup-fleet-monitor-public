# Backup Fleet Monitor

[English](README.md) | **Bahasa Indonesia**

Dashboard *read-only* yang menggabungkan status backup dari Proxmox VE, Veeam
(REST Enterprise Manager atau *push collector*) dan Synology Active Backup for
Business dalam satu tampilan. Terdiri dari API + poller (Go), PostgreSQL, dan UI
React/Vite di belakang nginx.

## Memulai dengan cepat

```bash
cp .env.example .env
chmod 600 .env
# isi POSTGRES_PASSWORD, ADMIN_BOOTSTRAP_PASSWORD (minimal 12 karakter) dan:
openssl rand -hex 32   # -> SECRET_KEY
docker compose up -d --build
```

Buka `http://<host>:8081` lalu masuk dengan admin bootstrap. Anda akan diminta
mengganti password pada login pertama.

## Catatan keamanan

- **TLS diterminasi di edge Anda** (F5, FortiWeb, Cloudflare, dll.). Container
  sendiri berbicara HTTP biasa dan tidak butuh sertifikat. Biarkan
  `COOKIE_SECURE=true` (bawaan): browser melihat HTTPS sehingga cookie `Secure`
  berfungsi. Gunakan `false` hanya untuk uji lokal langsung lewat HTTP. Atur HSTS
  di edge.
- **IP klien asli.** Di belakang proxy, semua request tampak berasal dari alamat
  proxy; atur `set_real_ip_from` di `web/nginx.conf` agar rate limit login dan
  audit log memakai IP klien yang sebenarnya.
- **Simpan cadangan `SECRET_KEY`.** Kredensial sumber disimpan terenkripsi
  dengannya (AES-256-GCM). Jika key hilang, semua kredensial harus dimasukkan
  ulang.
- Jangan pernah meng-commit `.env`. File ini sudah masuk `.gitignore`.
- Hanya container `web` yang dipublikasikan; `api` dan `postgres` tetap berada
  di jaringan Docker internal. Jangan membuka port API langsung, karena aplikasi
  mempercayai header `X-Real-IP` yang diatur oleh nginx bawaan.
- Pengguna dengan izin `sources.create`/`sources.update` dapat membuat server
  terhubung ke host HTTPS mana pun yang mereka isi (dibutuhkan untuk melakukan
  polling ke sistem backup Anda). Berikan izin ini hanya kepada administrator
  tepercaya, dan batasi lalu lintas keluar di level jaringan bila perlu.
- Alias node (IP/GUID menjadi FQDN) disimpan di tabel `node_aliases`. Isi
  sendiri dengan SQL; lihat
  `backend/cmd/server/migrations/008_node_aliases.sql`.

Laporkan kerentanan secara privat kepada maintainer, bukan lewat issue publik.

## Lisensi

MIT, lihat [LICENSE](LICENSE).
