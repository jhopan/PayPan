# PayPan

Payment gateway QRIS mandiri — satu binary Go, tanpa framework. Menerima notifikasi pembayaran dari [NotifListenPayment](https://github.com/jhopan/NotifListenPayment) (app Android yang merelay notif e-wallet), mencocokkan ke order, dan mengubah QRIS statis menjadi QRIS dinamis per order.

**Dokumentasi API untuk integrator: [API.md](API.md)** — buat invoice, cek status, webhook HMAC, contoh kode.

Dikembangkan oleh **JhopanStore**.

## Arsitektur

```
HP merchant (app NotifListenPayment) ──POST /api/notif──┐
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
- **Webhook** — POST `order.paid` + HMAC-SHA256 signature + retry
- **Telegram notify** — LUNAS / unmatched, multi-chat, retry
- **Keamanan** — rate limit login & API, security headers, HTML escape, audit log, backup DB otomatis 6 jam

## API

| Endpoint | Auth | Fungsi |
|---|---|---|
| `POST /api/notif` | Bearer (scope notif) | ingest notif dari device |
| `POST /api/order` | Bearer (scope order) | buat order → `{id, code, total, qr}` |
| `GET /api/order/{id}` | Bearer | status order |
| `GET /pay/{id}` | publik | halaman checkout + polling |
| `GET /pay/{id}/qr.png` | publik | gambar QR dinamis |
| `/admin/kasir` | session admin | kasir (input nominal → QR) |
| `/admin` | session | dashboard admin |

## Setup

```bash
# 1. siapkan payload QRIS statis (scan QR lo, simpan raw text)
echo "000201010211..." > server/qris_base.txt

# 2. build & jalankan
cd server
go build -o paypan-server.exe .
./paypan-server -addr :9090

# 3. buka admin
# http://localhost:9090/admin  (default: admin / admin123 — segera ganti)
```

## Deploy (production)

- Jalankan di balik reverse proxy / CF tunnel (HTTPS wajib)
- Ganti password admin, rotate token master
- Backup otomatis di `server/backup/`
- Systemd unit contoh:

```ini
[Service]
ExecStart=/opt/paypan/paypan-server -addr :9090
WorkingDirectory=/opt/paypan
Restart=always
User=paypan
```

## Verifikasi webhook (penerima)

```python
import hmac, hashlib
expected = hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
assert hmac.compare_digest(expected, request.headers["X-Paypan-Signature"])
```

---

## Kredit

**Dikembangkan oleh [JhopanStore](https://github.com/jhopan)**

© 2026 JhopanStore. Dibangun dengan bantuan AI (Hermes Agent).
