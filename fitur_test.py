"""Test 8 fitur baru: ringkasan app, tren, filter, cari, retry webhook, cetak, unmatched grup."""
import http.cookiejar, json, sqlite3, time, urllib.error, urllib.parse, urllib.request

BASE = "http://127.0.0.1:9090"
R = []


def ck(n, ok, e=""):
    R.append((n, ok))
    print(("PASS" if ok else "FAIL") + " | " + n + ((" -- " + e) if e and not ok else ""))


db = sqlite3.connect("paypan.db", check_same_thread=False)
db.execute("delete from apps where name in ('app-a','app-b')")
db.execute("insert into apps(name,token,scopes,active,created_at) values('app-a','pp_testappa123456','both',1,strftime('%s','now'))")
db.execute("insert into apps(name,token,scopes,active,created_at) values('app-b','pp_testappb123456','both',1,strftime('%s','now'))")
db.execute("delete from orders"); db.execute("delete from order_events"); db.execute("delete from unmatched")
db.commit()

tokA, tokB = "pp_testappa123456", "pp_testappb123456"


def api(path, body, tok):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=8) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode())


# buat 2 order app-a + 1 order app-b, lunasi semua
c, rA = api("/api/invoice", {"price": 11000, "label": "Pulsa A", "buyer": "A1"}, tokA)
c, rA2 = api("/api/invoice", {"price": 12000, "label": "Pulsa A2", "buyer": "A2"}, tokA)
c, rB = api("/api/invoice", {"price": 21000, "label": "Token B", "buyer": "B1"}, tokB)
for inv, tk in ((rA["data"], tokA), (rA2["data"], tokA), (rB["data"], tokB)):
    api("/api/notif", {"id": "t-" + inv["id"], "amount": inv["total"],
                       "text": f"Pembayaran QRIS statis diterima: Rp {inv['total']} di J Store."}, tk)

jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
op.open(BASE + "/admin")


def csrf():
    for c in jar:
        if c.name == "paypan_csrf":
            return c.value
    return ""


# 1. ringkasan per aplikasi
dash = op.open(BASE + "/admin").read().decode()
ck("dashboard: kartu 'Hari ini per aplikasi'", "Hari ini per aplikasi" in dash)
ck("ringkasan memuat app-a", "app-a" in dash)
ck("ringkasan memuat app-b", "app-b" in dash)

# 2. tren 7 hari
ck("dashboard: kartu 'Tren 7 hari'", "Tren 7 hari" in dash)
ck("tren punya bar grafik", "background:#1a7f37" in dash)

# 3. filter laporan per aplikasi
lapA = op.open(BASE + "/admin/laporan?app=app-a").read().decode()
ck("filter app=app-a hanya app-a", "app-a" in lapA and "app-b" not in lapA, "app-b muncul" if "app-b" in lapA else "")

# 4. pencarian
lapQ = op.open(BASE + "/admin/laporan?q=Token+B").read().decode()
ck("cari 'Token B' menemukan order", "Token B" in lapQ)

# 5. cetak / PDF
pdf = op.open(BASE + "/admin/laporan/cetak").read().decode()
ck("halaman cetak render", "Laporan Transaksi Paypan" in pdf)
ck("cetak memuat tabel transaksi", "Total" in pdf and "transaksi" in pdf)
ck("cetak memuat label", "Pulsa A" in pdf)

# 6. retry webhook
db.execute("delete from webhooks")
db.execute("insert into webhooks(app_rowid,url,secret,active,created_at) values((select max(rowid) from apps),'http://127.0.0.1:9/x','pp_w',1,strftime('%s','now'))")
db.commit()
oid = rA["data"]["id"]
resp = op.open(BASE + "/admin/webhook/retry", data=urllib.parse.urlencode({"order_id": oid, "csrf_token": csrf()}).encode())
q = db.execute("select count(*) from webhook_queue where order_id=?", (oid,)).fetchone()[0]
ck("retry webhook masukkan ke antrean", q >= 1, f"queue={q}")

# 7. unmatched per nominal
now = int(time.time())
db.execute("insert into unmatched(payment_id,amount,reason,received_at) values('u1',55001,'x',?)", (now,))
db.execute("insert into unmatched(payment_id,amount,reason,received_at) values('u2',55001,'x',?)", (now,))
db.execute("insert into unmatched(payment_id,amount,reason,received_at) values('u3',55002,'x',?)", (now,))
db.commit()
dash2 = op.open(BASE + "/admin").read().decode()
ck("dashboard: 'Unmatched per nominal'", "Unmatched per nominal" in dash2)
ck("grup tampilkan nominal 55.001", "55.001" in dash2)

# 8. Telegram berlabel (uji format pesan via fungsi internal tidak langsung -> cek audit/DB saja)
row = db.execute("select label,buyer from orders where id=?", (oid,)).fetchone()
ck("label+buyer ada untuk pesan TG", row and row[0] == "Pulsa A" and row[1] == "A1", str(row))

db.execute("delete from orders"); db.execute("delete from order_events")
db.execute("delete from unmatched"); db.execute("delete from webhook_queue")
db.execute("delete from webhooks")
db.execute("delete from apps where name in ('app-a','app-b')")
db.commit()

ok = sum(1 for _, o in R if o)
print(f"\n=== FITUR: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
