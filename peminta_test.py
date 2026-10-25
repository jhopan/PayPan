"""Test: nama aplikasi peminta tampil di detail, laporan, dan CSV."""
import http.cookiejar, json, sqlite3, time, urllib.error, urllib.parse, urllib.request

BASE = "http://127.0.0.1:9090"
R = []


def ck(n, ok, e=""):
    R.append((n, ok))
    print(("PASS" if ok else "FAIL") + " | " + n + ((" -- " + e) if e and not ok else ""))


db = sqlite3.connect("paypan.db", check_same_thread=False)

# siapkan dua app dengan token berbeda
db.execute("delete from apps where name in ('agenpulsa-uji','toko-uji')")
db.execute("insert into apps(name,token,scopes,active,created_at) values('agenpulsa-uji','pp_ujiangen1234567','both',1,strftime('%s','now'))")
db.execute("insert into apps(name,token,scopes,active,created_at) values('toko-uji','pp_ujitoko12345678','both',1,strftime('%s','now'))")
db.execute("delete from orders"); db.execute("delete from order_events")
db.commit()

tokA = "pp_ujiangen1234567"
tokB = "pp_ujitoko12345678"


def api(path, body, tok):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=8) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode())


# buat invoice dari dua app berbeda
c1, r1 = api("/api/invoice", {"price": 25000, "label": "Pulsa", "buyer": "A"}, tokA)
c2, r2 = api("/api/invoice", {"price": 35000, "label": "Token Listrik", "buyer": "B"}, tokB)
ck("invoice app A dibuat", c1 == 200, str(r1)[:80])
ck("invoice app B dibuat", c2 == 200, str(r2)[:80])
idA, idB = r1["data"]["id"], r2["data"]["id"]

rowA = db.execute("select created_by from orders where id=?", (idA,)).fetchone()
nameA = db.execute("select name from apps where rowid=?", (rowA[0],)).fetchone()
ck("created_by app A = agenpulsa-uji", nameA and nameA[0] == "agenpulsa-uji", str(nameA))

# lunasi keduanya
for inv, tk in ((r1["data"], tokA), (r2["data"], tokB)):
    api("/api/notif", {"id": "app-" + inv["id"], "amount": inv["total"],
                       "text": f"Pembayaran QRIS statis diterima: Rp {inv['total']} di J Store."}, tk)

jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
op.open(BASE + "/admin")

# detail transaksi
htmlA = op.open(BASE + f"/admin/tx/{idA}").read().decode()
ck("detail punya baris 'Diminta oleh'", "Diminta oleh" in htmlA)
ck("detail A tampilkan agenpulsa-uji", "agenpulsa-uji" in htmlA)
htmlB = op.open(BASE + f"/admin/tx/{idB}").read().decode()
ck("detail B tampilkan toko-uji", "toko-uji" in htmlB)

# laporan
lap = op.open(BASE + "/admin/laporan").read().decode()
ck("laporan punya kolom 'Diminta oleh'", "Diminta oleh" in lap)
ck("laporan tampilkan agenpulsa-uji", "agenpulsa-uji" in lap)
ck("laporan tampilkan toko-uji", "toko-uji" in lap)

# CSV
csv = op.open(BASE + "/admin/export").read().decode("utf-8-sig")
ck("CSV punya header 'Diminta oleh'", "Diminta oleh" in csv)
ck("CSV memuat agenpulsa-uji", "agenpulsa-uji" in csv)
ck("CSV memuat toko-uji", "toko-uji" in csv)

# order tanpa app (kasir) -> "—"
now = int(time.time())
db.execute("insert into orders(id,price,code,total,status,created_at,expires_at,created_by,label) values('noapp',10000,9,10009,'paid',?,?,0,'kasir')", (now, now + 300))
db.commit()
h = op.open(BASE + "/admin/tx/noapp").read().decode()
ck("order tanpa app tampil '—'", "Diminta oleh" in h and "—" in h)

# bersihkan
db.execute("delete from orders"); db.execute("delete from order_events")
db.execute("delete from apps where name in ('agenpulsa-uji','toko-uji')")
db.commit()

ok = sum(1 for _, o in R if o)
print(f"\n=== PEMINTA: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
