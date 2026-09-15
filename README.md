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
- **Web admin** (`/admin`) — Dashboard, Kasir, Aplikasi & Token, Log, Laporan, Backup & Restore, Konfigurasi
- **Webhook** — POST `order.paid` + HMAC-SHA256 per-webhook + retry 2x; URL dan secret dikelola dari Konfigurasi
- **Telegram notify** — LUNAS / unmatched, multi-chat, retry, alert kegagalan notif
- **Backup lokal** — tiap 1 jam overwrite `backup/paypan-hourly.db` (tanpa akumulasi file) + backup/download/restore dari web admin
- **Backup Google Sheets** — 1 spreadsheet per tahun, tab per bulan, blok per minggu; push manual atau otomatis harian setelah dikonfigurasi
- **Keamanan** — rate limit login & API (401 token salah, 429 limit), janitor eviction, security headers, HTML escape, audit log, QRIS hash pinning anti-tamper

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

Installer otomatis: cek dependencies → download binary siap pakai dari GitHub Releases → systemd service → setup QRIS → pilih tunnel (Cloudflare/ngrok/Caddy/Nginx) → pasang menu kontrol `menupaypan`.

Setelah terpasang, ketik `menupaypan` di terminal mana pun (auto-buka menu):

```
sudo menupaypan          # start/stop/restart/log/update/uninstall + tunnel
sudo menupaypan install  # jalankan ulang installer (repair)
```

## Setup (lokal)

```bash
# 1. siapkan payload QRIS statis (scan QR kamu, simpan raw text)
echo "000201010211..." > qris_base.txt

# 2. build & jalankan
go build -o paypan-server .
./paypan-server -addr :9090

# 3. buka admin
# http://localhost:9090/admin  (default: admin / admin123 — WAJIB ganti setelah login pertama)
```

> **Kredensial default** (`admin`/`admin123` dan token seed `master`) hanya ada saat first-run
> untuk akses pertama. Keduanya wajib diganti sebelum dipakai — lihat [API.md § Token & secret](API.md#7-token--secret--format-keamanan).

## Download binary siap pakai

Binary Linux/Windows otomatis di-build oleh GitHub Actions:
**[Releases → v1.1.1](https://github.com/jhopan/PayPan/releases/latest)**

## Deploy (production VPS)

**Cara termudah:** installer di atas (1 perintah) — sudah termasuk systemd + tunnel + menu kontrol.

Checklist production:

- Jalankan di balik **CF tunnel / reverse proxy** (HTTPS wajib)
- Ganti password admin (`admin123` default) + rotate token `master` dari menu Aplikasi
- Simpan token di env var / secret manager — jangan hardcode di source
- Backup lokal otomatis per jam di `backup/paypan-hourly.db` (satu file ditimpa, tidak menumpuk)
- Untuk restore atau download database, buka menu **Backup** di web admin (wajib login)
- Backup Google Sheets bersifat opsional; konfigurasi dari **Konfigurasi → Backup Google Sheets**
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

## Webhook (penerima)

Tambahkan URL penerima dari **Konfigurasi → Webhook**. PayPan membuat secret berbeda untuk setiap URL dan menampilkannya di tabel webhook.

Saat invoice `paid`, PayPan mengirim `POST` JSON berikut:

```text
X-Paypan-Event: order.paid
X-Paypan-Signature: <hex HMAC-SHA256>
X-Paypan-Hook-ID: <id webhook>
```

```python
import hmac, hashlib
expected = hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
assert hmac.compare_digest(expected, request.headers["X-Paypan-Signature"])
```

`secret` = secret per-webhook dari tabel Konfigurasi, bukan token aplikasi dan bukan global. Gagal kirim diulang 2 kali dengan jeda 30 detik; riwayat pengiriman tersimpan di detail transaksi.

---

## Kredit

**Dikembangkan oleh [JhopanStore](https://github.com/jhopan)**

© 2026 JhopanStore
