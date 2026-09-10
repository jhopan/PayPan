# PayPan API Documentation

API untuk integrasi payment gateway PayPan dengan bot, website, atau aplikasi lain.

Base URL: `https://paypan.domain-anda` (atau `http://host:9090` saat dev)

Autentikasi: **Bearer token** di header `Authorization`. Token dibuat di admin → **Aplikasi & Token**. Tiap aplikasi punya token sendiri dengan scope:

| Scope | Bisa |
|---|---|
| `notif` | mengirim notifikasi (dipakai app HP, bukan untuk integrator) |
| `order` | membuat/membaca/membatalkan invoice |
| `both` | semua di atas |

---

## Format respons (seragam)

```json
// sukses
{"ok": true, "data": { ... }}

// gagal
{"ok": false, "error": "pesan kesalahan"}
```

Error HTTP yang mungkin: `400` (input salah), `401` (token salah/scope kurang), `404` (tidak ditemukan), `405` (method salah), `409` (status tidak sesuai — mis. cancel invoice yang sudah paid), `429` (rate limit, 60 req/menit/token), `503` (semua kode unik sedang dipakai, coba lagi).

---

## 1. Buat invoice

```
POST /api/invoice
Authorization: Bearer <token scope order>
Content-Type: application/json

{"price": 25000, "label": "paket premium"}
```

`price` **minimal Rp 1.000** (3 digit terakhir dipakai sebagai kode unik), maksimal Rp 9.000.000.

Respons:

```json
{"ok": true, "data": {
  "id": "b327f2d9f96aba5c",
  "price": 25000,
  "code": 12,
  "total": 25012,
  "status": "pending",
  "expires_at": 1788966000,
  "pay_url": "/pay/b327f2d9f96aba5c",
  "qr_url": "/pay/b327f2d9f96aba5c/qr.png",
  "qr": "0002010102122661..."
}}
```

Penjelasan:

| Field | Arti |
|---|---|
| `code` | kode unik 3 digit (001-999) yang menempel di total — inilah yang membuat pembayaran bisa dicocokkan otomatis |
| `total` | yang harus dibayar customer = `price + code` |
| `pay_url` | halaman checkout siap tampil (QR + polling status otomatis) |
| `qr_url` | gambar QR dinamis (PNG) untuk di-embed sendiri |
| `qr` | payload mentah QRIS dinamis (kalau mau render QR sendiri) |
| `expires_at` | unix timestamp — invoice hangus setelah **5 menit** |

Customer scan QR → e-wallet menampilkan nominal `total` yang sudah terisi → bayar. **Customer tidak perlu mengetik nominal.**

---

## 2. Cek status invoice

```
GET /api/invoice/{id}
Authorization: Bearer <token scope order>
```

Respons:

```json
{"ok": true, "data": {
  "id": "b327f2d9f96aba5c",
  "status": "paid",
  "price": 25000, "code": 12, "total": 25012,
  "created_at": 1788965400,
  "expires_at": 1788966000,
  "paid_at": 1788965650,
  "pay_url": "/pay/b327f2d9f96aba5c",
  "qr_url": "/pay/b327f2d9f96aba5c/qr.png"
}}
```

Status yang mungkin:

| Status | Arti |
|---|---|
| `pending` | menunggu pembayaran (max 5 menit) |
| `paid` | lunas — notifikasi pembayaran diterima & dicocokkan |
| `expired` | hangus (5 menit lewat tanpa bayar) — kode balik ke pool setelah cooldown 2 jam |
| `refunded` | dana sudah dikembalikan manual oleh merchant |

Cara terbaik tahu invoice lunas **bukan polling**, tapi **webhook** (bagian 5). Polling hanya cadangan.

---

## 3. Batalkan invoice

```
POST /api/invoice/{id}/cancel
Authorization: Bearer <token scope order>
```

Respons: `{"ok": true, "data": {"id": "...", "status": "expired"}}`

Syarat: status masih `pending`. Kode unik langsung kembali ke pool. Invoice yang sudah `paid` tidak bisa dibatalkan (lihat refund).

---

## 4. Refund invoice (catatan administratif)

```
POST /api/invoice/{id}/refund
Authorization: Bearer <token scope order>
```

Respons: `{"ok": true, "data": {"id": "...", "status": "refunded"}}`

Syarat: status `paid`.

**Penting:** QRIS statis tidak punya mekanisme refund otomatis. Endpoint ini hanya **mencatat** bahwa merchant sudah mengembalikan dana secara manual (transfer via e-wallet). Status berubah `paid` → `refunded` agar laporan pendapatan tetap akurat.

---

## 5. Webhook — notifikasi invoice lunas

Daftarkan URL di admin → **Konfigurasi → Webhook**. Setiap invoice jadi `paid`, PayPan mengirim:

```
POST <url-anda>
Content-Type: application/json
X-Paypan-Event: order.paid
X-Paypan-Signature: <hex HMAC-SHA256>

{"event":"order.paid","order":{"id":"...","price":25000,"code":12,"total":25012,"paid_at":1788965650}}
```

**Verifikasi (wajib) di sisi penerima** — pastikan request benar-benar dari PayPan:

```python
import hmac, hashlib

def verify(body_bytes: bytes, signature: str, secret: str) -> bool:
    expected = hmac.new(secret.encode(), body_bytes, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature)

# secret = per-webhook, 16 karakter acak (pp_ + [a-zA-Z0-9]).
# Dilihat & di-rotate dari admin → Konfigurasi → Webhook.
# JANGAN pakai satu secret untuk semua webhook; tiap webhook punya secret sendiri.
```

- Signature dihitung dari **raw request body** (sebelum di-parse).
- Gagal kirim → PayPan retry 2x (jeda 30 detik). Semua percobaan tercatat.
- Uji payload manual per order ada di admin → **Transaksi Lunas** → detail.

---

## 6. Rate limit & kode error

- **60 request/menit** per token. Lebih dari itu → `429`.
- Maksimal **50 invoice pending aktif** per token (anti slot-filling). Invoice expired otomatis bebas dari hitungan.
- Login admin: 5x/10 menit per IP.

| HTTP | Arti |
|---|---|
| 400 | Body/format salah, atau harga di luar batas |
| 401 | Token salah, tidak aktif, atau scope kurang |
| 404 | Invoice/order tidak ditemukan |
| 405 | HTTP method salah |
| 409 | Aksi bentrok dengan status (mis. cancel invoice paid) |
| 429 | Rate limit (60 req/menit) atau lebih dari 50 invoice pending aktif dari token yang sama |
| 503 | Semua kode unik sedang dipakai — coba lagi beberapa detik |

---

## 7. Token & secret — format keamanan

Semua kredensial yang dibuat PayPan mengikuti format sama:

```
pp_ + 16 karakter acak [a-zA-Z0-9]     (≈95 bit entropi, crypto/rand)
contoh bentuk: pp_xK4mT9qLw2RbN7cE     (contoh bentuk saja — jangan dipakai)
```

| Kredensial | Dibuat di | Rotasi |
|---|---|---|
| Token API per-app | admin → Aplikasi & Token | tombol Rotate (token lama langsung mati) |
| Secret webhook per-hook | otomatis saat webhook ditambahkan | hapus + tambah ulang webhook |
| Password admin | admin → Konfigurasi → Login Admin | kapan saja |

Praktik aman:
- 1 token per aplikasi — jangan share token antar app/bot/website
- Simpan token di env var / secret manager, **jangan hardcode** di source code
- Kalau token bocor → Rotate instan dari admin, app lain tidak terpengaruh

---

## 8. Contoh lengkap

Lihat [`examples/integrator.py`](examples/integrator.py) — fungsi siap pakai `create_invoice`, `get_invoice`, `cancel_invoice`, `wait_paid` + dokumentasi verifikasi webhook.

Alur minimal sebuah toko:

```
1. Customer checkout di website/bot
2. Bot/website → POST /api/invoice {price: 25000}
3. Tampilkan pay_url atau qr_url ke customer
4. Customer bayar QRIS (nominal terisi otomatis)
5. HP merchant dapat notif → app NotifListenPayment kirim ke PayPan
6. PayPan match by total → invoice paid
7. PayPan → webhook order.paid ke website → website tandai lunas
```

Langkah 5-7 butuh **app NotifListenPayment ter-install di HP merchant** (lihat repo [NotifListen-Payment](https://github.com/jhopan/NotifListen-Payment)) dan **GoPay Merchant terlogin** di HP tersebut.
