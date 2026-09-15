# PayPan — Aturan Kerja

## Wajib sebelum klaim selesai

- Selalu uji perubahan secara lokal terlebih dahulu: build, lalu jalankan smoke test pada jalur utama yang diubah.
- Jangan klaim fitur/fix selesai sebelum ada output uji nyata.
- Untuk perubahan UI/admin, uji halaman lokal dengan autentikasi yang sesuai.
- Untuk API/webhook, lakukan simulasi request lokal dan cek hasil database/log yang relevan.

## Deploy

- JANGAN pernah deploy ke laptop-debian, VPS, Cloudflare Tunnel, atau sistem remote lain tanpa instruksi eksplisit dari pengguna.
- Perintah seperti build, commit, push, atau CI hijau BUKAN izin deploy.
- Jika pengguna hanya meminta edit, build, test, atau penjelasan: tetap di lokal.
- Setelah uji lokal lulus, laporkan hasil dan tunggu instruksi deploy eksplisit.
