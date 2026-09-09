import urllib.request, urllib.parse, json, sqlite3
# Login + buat invoice dulu (server belum jalan, jadi restore dari QR di db)
db = sqlite3.connect('paypan.db')
# ambil QR valid terakhir dari invoice yang pernah dibuat sebelum tamper (dari audit / order)
# QR disimpan hanya di response API, tidak di DB. Tapi qris_hash pinned = hash dari payload ASLI.
# Kita coba payload dari qris_base sebelum tamper: tidak ada backup. 
# Alternatif: gunakan QR dari /pay/{id} response lama? Tidak tersimpan.
# PALING MUDAH: server membaca qris_base.txt — kita tahu payload asli masih bisa 
# direkonstruksi dari QR invoice pertama yang pernah kita lihat di log httpbin.
# Tapi tidak tersimpan. Jadi: tanya user? Tidak — QRIS statis bisa di-scan ulang.
print("RESTORE MANUAL DIPERLUKAN: scan ulang QRIS statis lo, simpan ke qris_base.txt")
print("Atau: server menyimpan pinned hash, bukan payload. Payload hanya di file.")
