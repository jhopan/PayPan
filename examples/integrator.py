"""
Contoh integrasi PayPan API — buat invoice + verifikasi webhook HMAC.

Cara pakai:
  export PAYPAN_URL="https://paypan.domain-anda"
  export PAYPAN_TOKEN="pp_xxxxxxxx..."     # token scope "order" dari admin
  export PAYPAN_WEBHOOK_SECRET="..."       # sama dengan settings webhook_secret di admin
  python integrator.py

Endpoint:
  POST /api/invoice            body {price, label}   -> {ok, data:{id, total, pay_url, qr_url}}
  GET  /api/invoice/{id}                              -> {ok, data:{status, total, ...}}
  POST /api/invoice/{id}/cancel                        -> batalkan pending
  POST /api/invoice/{id}/refund                        -> tandai refunded (setelah transfer balik)

Webhook (saat invoice LUNAS), kirim POST JSON ke URL yang terdaftar di admin:
  header X-Paypan-Event     = "order.paid"
  header X-Paypan-Signature = hex(HMAC_SHA256(webhook_secret, body))
  body  {event:"order.paid", order:{id,price,code,total,paid_at}}

Verifikasi di sisi penerima (Flask):
  expected = hmac.new(secret.encode(), request.get_data(), hashlib.sha256).hexdigest()
  if not hmac.compare_digest(expected, request.headers.get("X-Paypan-Signature","")):
      abort(403)
"""
import os, json, hmac, hashlib, urllib.request

URL = os.environ["PAYPAN_URL"]
TOKEN = os.environ["PAYPAN_TOKEN"]


def api(method, path, body=None):
    req = urllib.request.Request(
        URL + path,
        data=json.dumps(body).encode() if body else None,
        headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"},
        method=method,
    )
    resp = json.load(urllib.request.urlopen(req, timeout=15))
    if not resp.get("ok"):
        raise RuntimeError(resp.get("error", "unknown"))
    return resp["data"]


def create_invoice(price, label=""):
    """Buat invoice QRIS. Return dict: id, total, pay_url, qr_url, expires_at."""
    return api("POST", "/api/invoice", {"price": price, "label": label})


def get_invoice(invoice_id):
    """Cek status invoice: pending | paid | expired | refunded."""
    return api("GET", "/api/invoice/" + invoice_id)


def cancel_invoice(invoice_id):
    """Batalkan invoice pending (kode balik ke pool)."""
    return api("POST", "/api/invoice/%s/cancel" % invoice_id)


def wait_paid(invoice_id, timeout=300):
    """Polling status sampai paid/expired (untuk integrasi tanpa webhook)."""
    import time
    deadline = time.time() + timeout
    while time.time() < deadline:
        inv = get_invoice(invoice_id)
        if inv["status"] != "pending":
            return inv
        time.sleep(3)
    raise TimeoutError("invoice belum lunas sampai timeout")


if __name__ == "__main__":
    inv = create_invoice(25000, "contoh tagihan")
    print("Invoice dibuat:")
    print("  ID      :", inv["id"])
    print("  Total   : Rp", inv["total"], "(harga + kode unik)")
    print("  Bayar di:", URL + inv["pay_url"])
    print("Cek status : get_invoice('" + inv["id"] + "')")
