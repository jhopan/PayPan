"""Test dashboard: kolom Untuk apa / Pemesan / Diminta oleh tampil."""
import http.cookiejar, json, sqlite3, time, urllib.error, urllib.parse, urllib.request

BASE = "http://127.0.0.1:9090"
R = []


def ck(n, ok, e=""):
    R.append((n, ok))
    print(("PASS" if ok else "FAIL") + " | " + n + ((" -- " + e) if e and not ok else ""))


db = sqlite3.connect("paypan.db", check_same_thread=False)
db.execute("delete from apps where name='agenpulsa-dash'")
db.execute("insert into apps(name,token,scopes,active,created_at) values('agenpulsa-dash','pp_dashagen1234567','both',1,strftime('%s','now'))")
db.execute("delete from orders"); db.commit()
tok = "pp_dashagen1234567"

req = urllib.request.Request(BASE + "/api/invoice",
    data=json.dumps({"price": 6500, "label": "Pulsa Telkomsel 10rb", "buyer": "08123456789"}).encode(),
    headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, method="POST")
inv = json.loads(urllib.request.urlopen(req, timeout=8).read().decode())["data"]

jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
html = op.open(BASE + "/admin").read().decode()

ck("dashboard punya kolom 'Untuk apa'", "Untuk apa" in html)
ck("dashboard punya kolom 'Pemesan'", "Pemesan" in html)
ck("dashboard punya kolom 'Diminta oleh'", "Diminta oleh" in html)
ck("dashboard tampilkan label", "Pulsa Telkomsel 10rb" in html)
ck("dashboard tampilkan buyer", "08123456789" in html)
ck("dashboard tampilkan nama app", "agenpulsa-dash" in html)
ck("dashboard tampilkan total 6.501", "6.501" in html or "6501" in html)

db.execute("delete from orders"); db.execute("delete from apps where name='agenpulsa-dash'")
db.commit()

ok = sum(1 for _, o in R if o)
print(f"\n=== DASHBOARD: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
