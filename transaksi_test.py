"""Test fitur transaksi: label/buyer, riwayat status, ekspor CSV, limit global."""
import http.cookiejar, io, json, sqlite3, time, urllib.error, urllib.parse, urllib.request

BASE = "http://127.0.0.1:9090"
R = []


def ck(n, ok, e=""):
    R.append((n, ok))
    print(("PASS" if ok else "FAIL") + " | " + n + ((" -- " + e) if e and not ok else ""))


db = sqlite3.connect("paypan.db", check_same_thread=False)
tok = db.execute("select token from apps where name='master'").fetchone()[0]


def api(path, body):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=8) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode())


jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
op.open(BASE + "/admin")


def csrf():
    for c in jar:
        if c.name == "paypan_csrf":
            return c.value
    return ""


db.execute("delete from orders"); db.execute("delete from order_events")
db.commit()

# ---- 1. label & buyer tersimpan ----
c, r = api("/api/invoice", {"price": 15000, "label": "Pulsa Telkomsel 10rb", "buyer": "08123456789"})
inv = r["data"]
row = db.execute("select label,buyer from orders where id=?", (inv["id"],)).fetchone()
ck("label tersimpan", row and row[0] == "Pulsa Telkomsel 10rb", str(row))
ck("buyer tersimpan", row and row[1] == "08123456789", str(row))

# ---- 2. riwayat status: notif match -> event tercatat ----
api("/api/notif", {"id": "tx-" + inv["id"], "amount": inv["total"],
                   "text": f"Pembayaran QRIS statis diterima: Rp {inv['total']} di J Store."})
ev = db.execute("select from_status,to_status,actor from order_events where order_id=? order by id desc limit 1", (inv["id"],)).fetchone()
ck("event paid tercatat", ev and ev[1] == "paid" and ev[0] == "pending", str(ev))
ck("actor system untuk notif", ev and ev[2] == "system", str(ev))

# ---- 3. refund -> event tercatat ----
op.open(BASE + f"/admin/tx/{inv['id']}", data=urllib.parse.urlencode({"act": "refund", "csrf_token": csrf()}).encode())
ev = db.execute("select from_status,to_status,actor from order_events where order_id=? order by id desc limit 1", (inv["id"],)).fetchone()
ck("event refund tercatat", ev and ev[0] == "paid" and ev[1] == "refunded", str(ev))
ck("actor admin untuk refund", ev and ev[2] == "admin", str(ev))

# ---- 4. riwayat tampil di halaman detail ----
html = op.open(BASE + f"/admin/tx/{inv['id']}").read().decode()
ck("halaman detail punya Riwayat status", "Riwayat status" in html)
ck("riwayat tampilkan paid->refunded", "paid" in html and "refunded" in html)
ck("detail tampilkan label", "Pulsa Telkomsel 10rb" in html)
ck("detail tampilkan buyer", "08123456789" in html)

# ---- 5. limit global pending ----
db.execute("delete from orders"); db.commit()
saved = db.execute("select value from settings where key='tuning_pending_limit'").fetchone()
# set limit 3 untuk uji
op.open(BASE + "/admin/config", data=urllib.parse.urlencode({"act": "tuning", "invoice_expiry": "300",
    "code_cooldown": "7200", "paid_cooldown": "86400", "pending_limit": "3", "csrf_token": csrf()}).encode())
codes = []
for i in range(4):
    cc, _ = api("/api/invoice", {"price": 20000 + i * 1000})
    codes.append(cc)
ck("limit global 3: req ke-4 ditolak 429", codes[:3] == [200, 200, 200] and codes[3] == 429, str(codes))
# kembalikan default
op.open(BASE + "/admin/config", data=urllib.parse.urlencode({"act": "tuning", "invoice_expiry": "300",
    "code_cooldown": "7200", "paid_cooldown": "86400", "pending_limit": "200", "csrf_token": csrf()}).encode())

# ---- 6. ekspor CSV ----
db.execute("delete from orders"); db.commit()
now = int(time.time())
db.execute("insert into orders(id,price,code,total,status,created_at,expires_at,paid_at,label,buyer) values('exp1',10000,5,10005,'paid',?,?,?,'Kaos Polos','Budi')", (now, now + 300, now))
db.execute("insert into orders(id,price,code,total,status,created_at,expires_at,label,buyer) values('exp2',20000,7,20007,'expired',?,?,'Topup Game','Siti')", (now, now + 300))
db.commit()
resp = op.open(BASE + "/admin/export?from=" + time.strftime("%Y-%m-%d", time.localtime(now - 86400)) + "&to=" + time.strftime("%Y-%m-%d", time.localtime(now)))
body = resp.read().decode("utf-8-sig")
ck("CSV content-type", "text/csv" in resp.headers.get("Content-Type", ""), resp.headers.get("Content-Type"))
ck("CSV ada header kolom", "ID,Waktu dibuat,Status" in body, body[:100])
ck("CSV memuat exp1", "exp1" in body)
ck("CSV memuat exp2", "exp2" in body)
ck("CSV memuat label", "Kaos Polos" in body and "Topup Game" in body)
ck("CSV memuat buyer", "Budi" in body and "Siti" in body)
ck("CSV punya link checkout", "/pay/exp1" in body)

# filter status
resp2 = op.open(BASE + "/admin/export?status=paid")
body2 = resp2.read().decode("utf-8-sig")
ck("filter status=paid hanya paid", "exp1" in body2 and "exp2" not in body2, body2[:200])

# anonim ditolak
class NoRedir(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None
try:
    code = urllib.request.build_opener(NoRedir).open(BASE + "/admin/export", timeout=5).status
except urllib.error.HTTPError as e:
    code = e.code
ck("ekspor anonim ditolak 302", code == 302, str(code))

db.execute("delete from orders"); db.execute("delete from order_events"); db.commit()

ok = sum(1 for _, o in R if o)
print(f"\n=== TRANSAKSI: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
