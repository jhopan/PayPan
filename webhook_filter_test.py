"""
Test webhook filter per app:
- invoice dari App A → hanya webhook App A yang dapat notif
- invoice dari kasir (created_by=0) → semua webhook aktif dapat notif
- payload berisi label, buyer, created_by
"""
import http.server, threading, json, time, requests, sqlite3, os, sys

BASE = "http://127.0.0.1:9090"
ADMIN = ("admin", "admin123")

received: dict[str, list[dict]] = {}
lock = threading.Lock()

class WebhookHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n))
        port = self.server.server_address[1]
        with lock:
            received.setdefault(str(port), []).append(body)
        self.send_response(200)
        self.end_headers()

def start_receiver(port):
    srv = http.server.HTTPServer(("127.0.0.1", port), WebhookHandler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    return srv

def admin_post(path, data=None, json_data=None):
    s = requests.Session()
    # login
    r = s.post(f"{BASE}/admin/login", data={"user": "admin", "pass": "admin123"}, allow_redirects=True)
    assert r.status_code == 200, f"login fail {r.status_code}"
    if json_data is not None:
        return s.post(f"{BASE}{path}", json=json_data)
    return s.post(f"{BASE}{path}", data=data)

def get_token(app_name):
    """ambil token app dari DB lokal"""
    db_path = os.path.join(os.path.dirname(__file__), "paypan.db")
    db = sqlite3.connect(db_path)
    row = db.execute("SELECT token FROM apps WHERE name=?", (app_name,)).fetchone()
    db.close()
    return row[0] if row else None

PASS = []; FAIL = []
def ok(name): PASS.append(name); print(f"  PASS {name}")
def fail(name, msg): FAIL.append(name); print(f"  FAIL {name}: {msg}")

print("=== webhook filter per app ===")

# mulai 2 receiver berbeda port
srv_a = start_receiver(18301)
srv_b = start_receiver(18302)
time.sleep(0.2)

# daftarkan 2 webhook berbeda app via DB langsung (lebih cepat dari web form)
db_path = os.path.join(os.path.dirname(__file__), "paypan.db")
db = sqlite3.connect(db_path)
# pastikan ada 2 app
app_a = db.execute("SELECT rowid FROM apps WHERE name='master'").fetchone()
app_b = db.execute("SELECT rowid FROM apps WHERE name='pan-wallet'").fetchone()
if not app_a or not app_b:
    print("SKIP: butuh 2 app (master + Website Toko) di DB")
    sys.exit(0)
rowid_a, rowid_b = app_a[0], app_b[0]

# hapus webhook test lama, tambah baru
db.execute("DELETE FROM webhooks WHERE url LIKE '%18301%' OR url LIKE '%18302%'")
now = int(time.time())
wh_a = db.execute("INSERT INTO webhooks(app_rowid,url,secret,active,created_at) VALUES(?,?,?,1,?)",
    (rowid_a, "http://127.0.0.1:18301/hook", "secret-a", now)).lastrowid
wh_b = db.execute("INSERT INTO webhooks(app_rowid,url,secret,active,created_at) VALUES(?,?,?,1,?)",
    (rowid_b, "http://127.0.0.1:18302/hook", "secret-b", now)).lastrowid
db.commit()

# ambil token App A
tok_a = db.execute("SELECT token FROM apps WHERE rowid=?", (rowid_a,)).fetchone()[0]
tok_b = db.execute("SELECT token FROM apps WHERE rowid=?", (rowid_b,)).fetchone()[0]
db.close()

# ---------- T1: invoice App A lunas → hanya port 18301 dapat notif ----------
received.clear()
r = requests.post(f"{BASE}/api/invoice",
    json={"price": 10001, "label": "Pulsa 10rb", "buyer": "08111"},
    headers={"Authorization": f"Bearer {tok_a}"})
assert r.status_code == 200, f"create invoice fail: {r.text}"
oid = r.json().get("id") or r.json()["data"]["id"]

# simulasi notif lunas
r2 = requests.post(f"{BASE}/api/notif",
    json={"id": f"notif-wftest-{oid[:8]}", "source": "BCA", "amount": (r.json().get("total") or r.json()["data"]["total"])},
    headers={"Authorization": f"Bearer {tok_a}"})
assert r2.status_code == 200

time.sleep(2)  # tunggu webhookWorker (5s tick), coba lagi kalau belum
for _ in range(10):
    with lock:
        if str(18301) in received or str(18302) in received:
            break
    time.sleep(1)

with lock:
    got_a = received.get("18301", [])
    got_b = received.get("18302", [])

if len(got_a) == 1 and len(got_b) == 0:
    ok("T1: App A invoice → hanya webhook App A")
else:
    fail("T1", f"port18301={len(got_a)} port18302={len(got_b)}")

# ---------- T2: payload berisi label, buyer, created_by ----------
if got_a:
    order = got_a[0].get("order", {})
    if order.get("label") == "Pulsa 10rb":
        ok("T2: payload.label benar")
    else:
        fail("T2", f"label={order.get('label')}")
    if order.get("buyer") == "08111":
        ok("T3: payload.buyer benar")
    else:
        fail("T3", f"buyer={order.get('buyer')}")
    if order.get("created_by"):
        ok("T4: payload.created_by ada")
    else:
        fail("T4", f"created_by={order.get('created_by')}")
else:
    fail("T2", "tidak ada payload diterima"); fail("T3", ""); fail("T4", "")

# ---------- T5: invoice App B lunas → hanya port 18302 ----------
received.clear()
r3 = requests.post(f"{BASE}/api/invoice",
    json={"price": 10002, "label": "Toko", "buyer": "pelanggan"},
    headers={"Authorization": f"Bearer {tok_b}"})
assert r3.status_code == 200
oid2 = r3.json().get("id") or r3.json()["data"]["id"]
r4 = requests.post(f"{BASE}/api/notif",
    json={"id": f"notif-wftest2-{oid2[:8]}", "source": "BCA", "amount": (r3.json().get("total") or r3.json()["data"]["total"])},
    headers={"Authorization": f"Bearer {tok_b}"})
assert r4.status_code == 200

for _ in range(12):
    with lock:
        if str(18301) in received or str(18302) in received:
            break
    time.sleep(1)

with lock:
    got_a2 = received.get("18301", [])
    got_b2 = received.get("18302", [])

if len(got_a2) == 0 and len(got_b2) == 1:
    ok("T5: App B invoice → hanya webhook App B")
else:
    fail("T5", f"port18301={len(got_a2)} port18302={len(got_b2)}")

# ---------- T6: kasir (created_by=0) → semua webhook dapat notif ----------
received.clear()
# buat order kasir langsung via DB
db = sqlite3.connect(db_path)
import random, hashlib
kasir_id = hashlib.sha256(str(random.random()).encode()).hexdigest()[:16]
kasir_total = 11099  # nominal unik untuk test kasir
db.execute("""INSERT INTO orders(id,price,code,total,status,expires_at,webhook_fired,created_by,label,buyer,created_at)
    VALUES(?,11096,3,?,?,strftime('%s','now')+300,0,0,'Kasir','',strftime('%s','now'))""",
    (kasir_id, kasir_total, "pending"))
db.commit()
db.close()

# simulasi bayar
r5 = requests.post(f"{BASE}/api/notif",
    json={"id": f"notif-kasir-{kasir_id[:8]}", "source": "BCA", "amount": kasir_total},
    headers={"Authorization": f"Bearer {tok_a}"})
# kasir: bisa match walau pakai token app lain

for _ in range(12):
    with lock:
        if str(18301) in received or str(18302) in received:
            break
    time.sleep(1)

with lock:
    got_a3 = received.get("18301", [])
    got_b3 = received.get("18302", [])

if len(got_a3) >= 1 and len(got_b3) >= 1:
    ok("T6: kasir (created_by=0) → semua webhook aktif")
else:
    fail("T6", f"port18301={len(got_a3)} port18302={len(got_b3)} (mungkin nominal duplikat, ok jika T1-T5 pass)")

# cleanup webhook test
db = sqlite3.connect(db_path)
db.execute("DELETE FROM webhooks WHERE id IN (?,?)", (wh_a, wh_b))
db.commit(); db.close()

srv_a.shutdown(); srv_b.shutdown()
print(f"\nHASIL: {len(PASS)}/{len(PASS)+len(FAIL)} PASS")
if FAIL: sys.exit(1)
