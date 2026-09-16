# PayPan — Environment Variables

Konfigurasi runtime lewat env. Aman untuk migrasi antar mesin: berhenti service,
pindah DB/binary, jalankan dengan env sama — data dan konfigurasi ikut.

## Prioritas

```text
flag eksplisit  >  env  >  default bawaan
```

Contoh: `-addr :9090` menimpa `PAYPAN_ADDR`. Tanpa flag, `PAYPAN_ADDR` menimpa
default `:8090`.

## Daftar variabel

| Variabel | Default | Fungsi |
|---|---|---|
| `PAYPAN_ADDR` | `:8090` | Address listen HTTP |
| `PAYPAN_DB` | `paypan.db` | Path file SQLite |
| `PAYPAN_TOKEN` | — | Seed token `master` awal (sekali, saat DB kosong) |
| `PAYPAN_TGTOKEN` | — | Telegram bot token awal (opsional) |
| `PAYPAN_TGCHAT` | — | Telegram chat id awal (opsional) |

## Contoh systemd

```ini
[Service]
Environment=PAYPAN_ADDR=:9090
Environment=PAYPAN_DB=/var/lib/paypan/paypan.db
ExecStart=/opt/paypan/paypan
```

## Contoh docker

```yaml
environment:
  - PAYPAN_ADDR=:9090
  - PAYPAN_DB=/data/paypan.db
volumes:
  - ./data:/data
```

## Catatan keamanan

- `PAYPAN_TOKEN`, `PAYPAN_TGTOKEN`, `PAYPAN_TGCHAT` hanya seed awal. Setelah
  DB berisi, auth asli baca tabel `apps` dan settings — nilai env diabaikan.
- Token aplikasi dan secret webhook dibuat/dirotasi dari web admin, tersimpan
  di DB — bukan env. Jangan taruh secret jangka panjang di env.
- DB yang menang atas semuanya untuk konfigurasi harian (QRIS, Telegram,
  webhook, Sheets, password admin).
