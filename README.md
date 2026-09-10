# PayPan

Payment gateway QRIS mandiri — satu binary Go, tanpa framework. Menerima notifikasi pembayaran dari [NotifListen-Payment](https://github.com/jhopan/NotifListen-Payment) (app Android yang merelay notif e-wallet), mencocokkan ke order, dan mengubah QRIS statis menjadi QRIS dinamis per order.

**Dokumentasi API untuk integrator: [API.md](API.md)** — buat invoice, cek status, webhook HMAC, contoh kode.

Dikembangkan oleh **JhopanStore**.

## Arsitektur

```
HP merchant (app NotifListen-Payment) ──POST /api/notif──┐
                                                         ▼
Customer → scan QRIS dinamis ───────────────────► ┌──────────────────┐
                                                  │  PayPan Server    │
Dashboard admin ◄───────────────────────────────► │  - ingest + match │
Bot Telegram    ◄───────────────────────────────► │  - SQLite         │
Webhook (site lain) ◄──────────────────────────── └──────────────────┘
```

## Fitur

- **QRIS dinamis dari statis** — inject tag 54 (nominal) + recompute CRC16 ke payload QRIS statis
- **Kode unik per level harga** — 1000→1001, 2000→2001; total diklaim eksklusif (UNIQUE partial index), race-proof
- **Match exact** — notif `amount` dicocokkan ke satu order pending; idempoten
- **Expiry 5 menit** + cooldown kode 2 jam (kode paid terpakai permanen) + limit 50 pending/token + QRIS hash pinning anti-tamper
- **Web admin** (`/admin`) — Dashboard, Kasir, Aplikasi & Token (scope per-app), Log, Laporan (rekap bulanan/harian + purge), Konfigurasi
- **Webhook** — POST `order.paid` + HMAC-SHA256 signature per-webhook + retry
- **Telegram notify** — LUNAS / unmatched, multi-chat, retry, alert kegagalan notif
- **Keamanan** — rate limit login & API (janitor eviction), security headers, HTML escape, audit log, backup DB otomatis 6 jam

## API

| Endpoint | Auth | Fungsi |
|---|---|---|
| `POST /api/notif` | Bearer (scope notif) | ingest notif dari device |
| `POST /api/order` | Bearer (scope order) | buat order → `{id, code, total, qr}` |
| `POST /api/invoice` | Bearer (scope order) | buat invoice (format respons standar `{ok,data}`) |
| `GET /api/invoice/{id}` | Bearer | status invoice |
| `POST /api/invoice/{id}/cancel` | Bearer | batal invoice pending |
| `POST /api/invoice/{id}/refund` | Bearer | tandai refund (administratif) |
| `GET /pay/{id}` | publik | halaman checkout + polling |
| `GET /pay/{id}/qr.png` | publik | gambar QR dinamis |
| `/admin/kasir` | session admin | kasir (input nominal → QR) |
| `/admin` | session | dashboard admin |

## Install 1 perintah (Linux VPS)

```bash
curl -fsSL https://raw.githubusercontent.com/jhopan/PayPan/master/install.sh | bash
```

Installer otomatis: cek dependencies → download binary siap pakai dari GitHub Releases → systemd service → setup QRIS → pilih tunnel (Cloudflare/ngrok/Caddy/Nginx) → pasang menu kontrol `paypan`.

Setelah terpasang, kontrol kapan saja:

```bash
sudo paypan          # menu: start/stop/restart/log/update/uninstall + tunnel
```

## Setup manual (tanpa installer)

```bash
# 1. siapkan payload QRIS statis (scan QR lo, simpan raw text)
echo "000201010211..." > qris_base.txt

# 2. build & jalankan
go build -o paypan-server .
./paypan-server -addr :9090

# 3. buka admin
# http://localhost:9090/admin  (default: admin / admin123 — segera ganti)
```

## Download binary siap pakai

Binary Linux/Windows otomatis di-build oleh GitHub Actions:
**[Releases → v1.1.1](https://github.com/jhopan/PayPan/releases/latest)**

## Deploy (production VPS)

**Cara termudah:** installer di atas (1 perintah) — sudah termasuk systemd + tunnel + menu kontrol.

Checklist production:

- Jalankan di balik **CF tunnel / reverse proxy** (HTTPS wajib)
- Ganti password admin (`admin123` default) + rotate token `master` dari menu Aplikasi
- Backup otomatis di folder `backup/` (rotasi 28 file ≈ 7 hari) — offsite opsional
- Verifikasi hash QRIS jalan: ubah `qris_base.txt` manual → server tolak invoice + kirim Telegram alarm

Systemd unit contoh:

```ini
[Unit]
Description=Paypan payment gateway
After=network.target

[Service]
ExecStart=/opt/paypan/paypan-linux-amd64 -addr :9090 -db /var/lib/paypan/paypan.db
WorkingDirectory=/opt/paypan
Restart=always
User=paypan
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/paypan

[Install]
WantedBy=multi-user.target
```

## CI/CD

GitHub Actions (`.github/workflows/build.yml`):
- Setiap push → `go vet` + build binary **linux-amd64** & **windows-amd64** + smoke test
- Push ke `master` → release otomatis tag `v1.1.1` (rolling) berisi binary + checksums SHA256

## Verifikasi webhook (penerima)

```python
import hmac, hashlib
expected = hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
assert hmac.compare_digest(expected, request.headers["X-Paypan-Signature"])
```

`secret` = per-webhook (dilihat di menu Konfigurasi admin), bukan global.

---

## Kredit

**Dikembangkan oleh [JhopanStore](https://github.com/jhopan)**

© 2026 JhopanStore. Dibangun dengan bantuan AI (Hermes Agent).
