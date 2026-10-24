"""E2E test fitur baru: unmatched action, webhook queue, healthz, clientIP."""
import hashlib
import hmac
import http.cookiejar
import http.server
import json
import sqlite3
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "http://127.0.0.1:9090"
DB = r"C:\Users\ACER\Documents\project\PayPan\paypan.db"
PORT = 18097
received = []
fail_mode = {"on": False}


class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n)
        if fail_mode["on"]:
            self.send_response(500)
            self.end_headers()
            return
        received.append((dict(self.headers), body))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def log_message(self, *a):
        pass


srv = http.server.HTTPServer(("127.0.0.1", PORT), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

db = sqlite3.connect(DB, check_same_thread=False)
tok = db.execute("select token from apps where name='master'").fetchone()[0]
R = []


def ck(name, ok, extra=""):
    R.append((name, ok))
    print(("PASS" if ok else "FAIL") + " | " + name + ((" -- " + extra) if extra and not ok else ""))


def api(path, body, token=None):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + (token or tok), "Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=8) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode())


jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
op.open(BASE + "/admin")  # cookie CSRF


def csrf():
    for c in jar:
        if c.name == "paypan_csrf":
            return c.value
    return ""


def admin_post(data):
    d = dict(data)
    d["csrf_token"] = csrf()
    return op.open(BASE + "/admin", data=urllib.parse.urlencode(d).encode()).read().decode()


# ---------- 1. healthz ----------
with urllib.request.urlopen(BASE + "/healthz", timeout=5) as r:
    h = json.loads(r.read().decode())
ck("healthz 200 + status ok", r.status == 200 and h.get("status") in ("ok", "degraded"), str(h))
ck("healthz lapor qris ok", h.get("qris") == "ok", str(h))

# ---------- 2. webhook queue: enqueue lalu worker kirim ----------
db.execute("delete from webhooks")
db.execute("insert into webhooks(app_rowid,url,secret,active,created_at) values((select max(rowid) from apps),?,?,1,strftime('%s','now'))",
           (f"http://127.0.0.1:{PORT}/hook", "pp_testsig1234567890"))
db.commit()

c, r = api("/api/invoice", {"price": 21000})
inv = r["data"]
db.execute("update orders set status='pending', expires_at=strftime('%s','now')+300 where id=?", (inv["id"],))
db.commit()
c, r = api("/api/notif", {"id": "q-" + inv["id"], "amount": inv["total"],
                          "text": f"Pembayaran QRIS statis diterima: Rp {inv['total']} di J Store."})
ck("notif -> paid", c == 200 and r.get("matched") is True, str(r))

# worker tick 5s: tunggu sampai antrean ATAU log terisi
queued = logged = 0
for _ in range(30):
    queued = db.execute("select count(*) from webhook_queue where order_id=?", (inv["id"],)).fetchone()[0]
    logged = db.execute("select count(*) from webhook_log where order_id=?", (inv["id"],)).fetchone()[0]
    if queued >= 1 or logged >= 1:
        break
    time.sleep(0.3)
ck("webhook masuk antrean/langsung terkirim", queued >= 1 or logged >= 1, f"queue={queued} log={logged}")

# worker tick 5s
for _ in range(20):
    if received:
        break
    time.sleep(0.5)
ck("worker kirim webhook", len(received) > 0, f"received={len(received)}")

if received:
    hdrs, body = received[0]
    exp = hmac.new(b"pp_testsig1234567890", body, hashlib.sha256).hexdigest()
    ck("HMAC tetap valid", hmac.compare_digest(hdrs.get("X-Paypan-Signature", ""), exp))

for _ in range(10):
    left = db.execute("select count(*) from webhook_queue where order_id=?", (inv["id"],)).fetchone()[0]
    if left == 0:
        break
    time.sleep(0.5)
ck("antrean bersih setelah sukses", left == 0, f"sisa={left}")

# ---------- 3. webhook retry saat penerima gagal ----------
received.clear()
fail_mode["on"] = True
c, r = api("/api/invoice", {"price": 22000})
inv2 = r["data"]
db.execute("update orders set expires_at=strftime('%s','now')+300 where id=?", (inv2["id"],))
db.commit()
api("/api/notif", {"id": "q2-" + inv2["id"], "amount": inv2["total"],
                   "text": f"Pembayaran QRIS statis diterima: Rp {inv2['total']} di J Store."})
time.sleep(8)
row = db.execute("select attempts from webhook_queue where order_id=?", (inv2["id"],)).fetchone()
ck("gagal kirim -> attempts naik & tetap di antrean", row is not None and row[0] >= 1, str(row))
fail_mode["on"] = False
db.execute("delete from webhook_queue")
db.commit()

# ---------- 4. unmatched: tombol cocokkan ----------
db.execute("delete from orders")
db.execute("delete from unmatched")
db.commit()
now = int(time.time())
# order expired 3 jam lalu (lewat cooldown) dengan total 23005
db.execute("insert into orders(id,price,code,total,status,created_at,expires_at) values('unm1',23000,5,23005,'expired',?,?)", (now - 3 * 3600, now - 3 * 3600))
db.execute("insert into unmatched(payment_id,amount,reason,received_at) values('pay-unm',23005,'no pending order',?)", (now,))
db.commit()

html = op.open(BASE + "/admin").read().decode()
ck("dashboard tampil tombol Cocokkan", "Cocokkan → unm1" in html or "Cocokkan" in html, html[html.find("Unmatched"):html.find("Unmatched") + 300] if "Unmatched" in html else "no card")
ck("dashboard tampil tombol Abaikan", "Abaikan" in html)

admin_post({"act": "unm_match", "pid": "pay-unm", "oid": "unm1"})
st = db.execute("select status from orders where id='unm1'").fetchone()
ck("unmatched match -> order paid", st is not None and st[0] == "paid", str(st))
left = db.execute("select count(*) from unmatched where payment_id='pay-unm'").fetchone()[0]
ck("baris unmatched terhapus", left == 0, f"sisa={left}")
aud = db.execute("select action from audit where action='unmatched.match'").fetchone()
ck("audit unmatched.match tercatat", aud is not None, str(aud))

# ---------- 5. unmatched: abaikan ----------
db.execute("insert into unmatched(payment_id,amount,reason,received_at) values('pay-ign',99999,'no pending order',?)", (now,))
db.commit()
admin_post({"act": "unm_ignore", "pid": "pay-ign"})
left = db.execute("select count(*) from unmatched where payment_id='pay-ign'").fetchone()[0]
ck("abaikan menghapus baris", left == 0, f"sisa={left}")

# ---------- 6. unmatched match menolak nominal beda ----------
db.execute("delete from orders")
db.execute("insert into orders(id,price,code,total,status,created_at,expires_at) values('unm2',24000,1,24001,'expired',?,?)", (now - 3 * 3600, now - 3 * 3600))
db.execute("insert into unmatched(payment_id,amount,reason,received_at) values('pay-x',24002,'x',?)", (now,))
db.commit()
admin_post({"act": "unm_match", "pid": "pay-x", "oid": "unm2"})
st = db.execute("select status from orders where id='unm2'").fetchone()
ck("nominal beda ditolak (order tetap expired)", st[0] == "expired", str(st))

# ---------- 7. clientIP tidak percaya XFF dari non-proxy ----------
req = urllib.request.Request(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "salah"}).encode(),
                             headers={"X-Forwarded-For": "9.9.9.9"})
try:
    urllib.request.urlopen(req, timeout=5)
except urllib.error.HTTPError:
    pass
time.sleep(0.3)
row = db.execute("select detail from audit where action='admin.login' order by id desc limit 1").fetchone()
ck("login tercatat dari IP asli, bukan XFF palsu", row is not None and "9.9.9.9" not in (row[0] or ""), str(row))

# cleanup
db.execute("delete from orders")
db.execute("delete from unmatched")
db.execute("delete from webhooks")
db.execute("delete from webhook_queue")
db.commit()

ok = sum(1 for _, o in R if o)
print(f"\n=== HASIL: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
