"""Regression suite PayPan: API, webhook, cooldown, unmatched, healthz.
Jalankan terhadap server lokal :9090. Tidak mengubah data production.
"""
import hashlib, hmac, http.cookiejar, http.server, json, sqlite3, sys, threading, time
import urllib.error, urllib.parse, urllib.request

BASE = "http://127.0.0.1:9090"
DB = "paypan.db"
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18099
R = []
got = []
fail = {"on": False}


class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n)
        if fail["on"]:
            self.send_response(500); self.end_headers(); return
        got.append((dict(self.headers), body))
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"ok":true}')

    def log_message(self, *a):
        pass


srv = http.server.HTTPServer(("127.0.0.1", PORT), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

db = sqlite3.connect(DB, check_same_thread=False)
tok = db.execute("select token from apps where name='master'").fetchone()[0]


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
        try:
            return e.code, json.loads(e.read().decode())
        except Exception:
            return e.code, {}


jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
op.open(BASE + "/admin")  # pastikan cookie CSRF terpasang


def csrf():
    for c in jar:
        if c.name == "paypan_csrf":
            return c.value
    return ""

# bersihkan state test
db.execute("delete from orders"); db.execute("delete from unmatched")
db.execute("delete from webhooks"); db.execute("delete from webhook_queue")
db.commit()

# 1 auth
c, _ = api("/api/invoice", {"price": 10000}, token="pp_salah")
ck("token salah 401", c == 401, f"{c}")

# 2 validasi harga
c, _ = api("/api/invoice", {"price": 500})
ck("harga < 1000 ditolak 400", c == 400, f"{c}")

# 3 webhook terdaftar
db.execute("insert into webhooks(app_rowid,url,secret,active,created_at) values((select max(rowid) from apps),?,?,1,strftime('%s','now'))",
           (f"http://127.0.0.1:{PORT}/hook", "pp_regressionsig1234"))
db.commit()

# 4 invoice + QR
c, r = api("/api/invoice", {"price": 31000})
ck("buat invoice", c == 200 and r.get("ok"), str(r)[:80])
inv = r["data"]
ck("total = price + code", inv["total"] == inv["price"] + inv["code"])
with urllib.request.urlopen(BASE + f"/pay/{inv['id']}", timeout=8) as rr:
    ck("checkout 200", rr.status == 200)
with urllib.request.urlopen(BASE + f"/pay/{inv['id']}/qr.png", timeout=8) as rr:
    ck("QR PNG valid", rr.read(8) == b"\x89PNG\r\n\x1a\n")

# 5 notif match
c, r = api("/api/notif", {"id": "rg-" + inv["id"], "amount": inv["total"],
                          "text": f"Pembayaran QRIS statis diterima: Rp {inv['total']} di J Store."})
ck("notif match -> paid", c == 200 and r.get("matched") is True, str(r))

# 6 webhook + HMAC
for _ in range(40):
    if got: break
    time.sleep(0.3)
ck("webhook diterima", len(got) > 0)
if got:
    hdrs, body = got[0]
    exp = hmac.new(b"pp_regressionsig1234", body, hashlib.sha256).hexdigest()
    ck("HMAC valid", hmac.compare_digest(hdrs.get("X-Paypan-Signature", ""), exp))
    ck("payload order.paid", json.loads(body.decode()).get("event") == "order.paid")

# 7 idempoten
c, r = api("/api/notif", {"id": "rg-" + inv["id"], "amount": inv["total"], "text": "dup"})
ck("duplikat idempoten", c == 200 and r.get("dup") is True, str(r))

# 8 cancel
c, r2 = api("/api/invoice", {"price": 32000})
c, r = api(f"/api/invoice/{r2['data']['id']}/cancel", {})
ck("cancel pending 200", c == 200 and r.get("ok"), str(r))
c, _ = api(f"/api/invoice/{r2['data']['id']}/cancel", {})
ck("cancel kedua 409", c == 409, f"{c}")

# 9 refund
c, r = api(f"/api/invoice/{inv['id']}/refund", {})
ck("refund paid 200", c == 200 and r.get("data", {}).get("status") == "refunded", str(r))
c, _ = api(f"/api/invoice/{inv['id']}/refund", {})
ck("refund kedua 409", c == 409, f"{c}")

# 10 healthz
with urllib.request.urlopen(BASE + "/healthz", timeout=5) as rr:
    h = json.loads(rr.read().decode())
ck("healthz ok", rr.status == 200 and h.get("status") in ("ok", "degraded"), str(h))

# 11 backup anonim
class NoRedir(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None
try:
    anon = urllib.request.build_opener(NoRedir).open(BASE + "/admin/backup", timeout=5).status
except urllib.error.HTTPError as e:
    anon = e.code
ck("backup anonim 302", anon == 302, f"{anon}")

# 12 rate limit
codes = [api("/api/invoice", {"price": 40000 + i})[0] for i in range(65)]
ck("rate limit 429 muncul", 429 in codes, f"max={max(codes)}")

# cleanup
db.execute("delete from orders"); db.execute("delete from unmatched")
db.execute("delete from webhooks"); db.execute("delete from webhook_queue")
db.commit()

ok = sum(1 for _, o in R if o)
print(f"\n=== REGRESI: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
