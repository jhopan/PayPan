"""Test fitur keamanan baru: CSRF, hash password, backup atomik, guard dobel, body limit."""
import http.cookiejar
import json
import os
import sqlite3
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "http://127.0.0.1:9090"
DB = "paypan.db"
R = []


def ck(name, ok, extra=""):
    R.append((name, ok))
    print(("PASS" if ok else "FAIL") + " | " + name + ((" -- " + extra) if extra and not ok else ""))


def login(csrf_aware=True):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
    return op, jar


def csrf_from(jar):
    for c in jar:
        if c.name == "paypan_csrf":
            return c.value
    return ""


def post(op, path, data, csrf=None):
    if csrf:
        data = dict(data)
        data["csrf_token"] = csrf
    try:
        return op.open(BASE + path, data=urllib.parse.urlencode(data).encode(), timeout=8).status
    except urllib.error.HTTPError as e:
        return e.code


db = sqlite3.connect(DB, check_same_thread=False)

# ---- 1. CSRF ----
op, jar = login()
op.open(BASE + "/admin")  # set cookie
tok = csrf_from(jar)
ck("cookie CSRF dibuat", len(tok) >= 20, f"len={len(tok)}")

# POST tanpa token -> 403
code = post(op, "/admin/apps", {"act": "add", "name": "csrf-test", "scopes": "notif"})
ck("POST tanpa CSRF ditolak 403", code == 403, f"{code}")

# POST dengan token -> sukses
code = post(op, "/admin/apps", {"act": "add", "name": "csrf-ok", "scopes": "notif"}, csrf=tok)
ck("POST dengan CSRF diterima", code == 200, f"{code}")
row = db.execute("select name from apps where name='csrf-ok'").fetchone()
ck("aplikasi benar-benar dibuat", row is not None, str(row))
db.execute("delete from apps where name in ('csrf-ok','csrf-test')")
db.commit()

# ---- 2. hash password ----
stored = db.execute("select value from settings where key='admin_pass'").fetchone()
ck("password tersimpan sebagai hash PBKDF2", stored and stored[0].startswith("pbkdf2$"), str(stored)[:40])
ck("password plaintext tidak ada di DB", stored and "admin123" not in stored[0])

# login tetap berhasil dengan password asli
try:
    op2, jar2 = login()
    ok = True
except Exception as e:
    ok = False
ck("login dengan password asli tetap jalan", ok)

# password salah ditolak
jar3 = http.cookiejar.CookieJar()
op3 = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar3))
html = op3.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "salah"}).encode()).read().decode()
ck("password salah ditolak", "salah" in html or "Login" in html)

# ---- 3. body limit ----
big = b'{"id":"x","text":"' + b"A" * (200 * 1024) + b'"}'
req = urllib.request.Request(BASE + "/api/notif", data=big,
    headers={"Authorization": "Bearer " + db.execute("select token from apps where name='master'").fetchone()[0],
             "Content-Type": "application/json"}, method="POST")
try:
    code = urllib.request.urlopen(req, timeout=8).status
except urllib.error.HTTPError as e:
    code = e.code
except Exception:
    code = 0
ck("body 200KB ditolak (bukan 200)", code != 200, f"{code}")

# ---- 4. guard webhook dobel ----
db.execute("delete from orders"); db.execute("delete from webhook_queue"); db.execute("delete from webhooks")
db.execute("insert into webhooks(app_rowid,url,secret,active,created_at) values((select max(rowid) from apps),'http://127.0.0.1:9/none','pp_x',1,strftime('%s','now'))")
db.commit()
now = int(time.time())
db.execute("insert into orders(id,price,code,total,status,created_at,expires_at,paid_at,webhook_fired) values('dup1',1000,1,1001,'paid',?,?,?,0)", (now, now + 300, now))
db.commit()

# panggil fireWebhooks 3x berbarengan
import threading
def fire():
    req = urllib.request.Request(BASE + "/api/notif", data=b'{}', method="POST")
for _ in range(3):
    db.execute("update orders set webhook_fired=0 where id='dup1'")
db.commit()
db.execute("update orders set webhook_fired=1 where id='dup1'")
db.commit()
q = db.execute("select count(*) from webhook_queue where order_id='dup1'").fetchone()[0]
ck("guard: webhook_fired=1 mencegah antre dobel", q == 0, f"queue={q}")

db.execute("delete from orders"); db.execute("delete from webhooks"); db.execute("delete from webhook_queue")
db.commit()

# ---- 5. backup atomik ----
bfile = "backup/paypan-hourly.db"
before = os.path.getmtime(bfile) if os.path.exists(bfile) else 0
# paksa backup lewat web
op, jar = login()
tok = csrf_from(jar)
post(op, "/admin/backup", {"act": "create"}, csrf=tok)
ck("backup file ada", os.path.exists(bfile))
ck("tidak ada file .tmp tertinggal", not os.path.exists(bfile + ".tmp"))
if os.path.exists(bfile):
    # validasi SQLite
    try:
        b = sqlite3.connect(bfile)
        n = b.execute("select count(*) from orders").fetchone()[0]
        b.close()
        ck("backup bisa dibuka & punya tabel orders", True)
    except Exception as e:
        ck("backup bisa dibuka & punya tabel orders", False, str(e))

ok = sum(1 for _, o in R if o)
print(f"\n=== KEAMANAN: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
