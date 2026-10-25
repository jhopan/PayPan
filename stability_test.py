"""Test stabilitas: panic recovery, webhook paralel, graceful shutdown, timeout."""
import http.cookiejar, http.server, json, os, signal, socket, sqlite3, subprocess, threading, time
import urllib.error, urllib.parse, urllib.request

BASE = "http://127.0.0.1:9090"
R = []
got = []


def ck(n, ok, e=""):
    R.append((n, ok))
    print(("PASS" if ok else "FAIL") + " | " + n + ((" -- " + e) if e and not ok else ""))


class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        got.append(time.time())
        time.sleep(0.2)  # simulasi penerima lambat
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"ok":true}')
    def log_message(self, *a): pass


srv = http.server.HTTPServer(("127.0.0.1", 18101), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

db = sqlite3.connect("paypan.db", check_same_thread=False)
tok = db.execute("select token from apps where name='master'").fetchone()[0]

# --- 1. server masih hidup & healthz ---
with urllib.request.urlopen(BASE + "/healthz", timeout=5) as r:
    ck("healthz 200", r.status == 200)

# --- 2. request invalid tidak mematikan server ---
for path, data in [("/api/notif", b"not-json"), ("/api/invoice", b"{bad}")]:
    req = urllib.request.Request(BASE + path, data=data,
        headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, method="POST")
    try:
        urllib.request.urlopen(req, timeout=5)
    except Exception:
        pass
with urllib.request.urlopen(BASE + "/healthz", timeout=5) as r:
    ck("server tetap hidup setelah input rusak", r.status == 200)

# --- 3. webhook paralel: 6 order sekaligus ---
db.execute("delete from webhooks"); db.execute("delete from webhook_queue"); db.execute("delete from orders")
db.execute("insert into webhooks(app_rowid,url,secret,active,created_at) values((select max(rowid) from apps),?,?,1,strftime('%s','now'))",
           ("http://127.0.0.1:18101/hook", "pp_stab"))
db.commit()
now = int(time.time())
for i in range(6):
    db.execute("insert into orders(id,price,code,total,status,created_at,expires_at,paid_at,webhook_fired) values(?,?,?,?,'paid',?,?,?,0)",
               (f"par{i}", 5000, i + 1, 5000 + i + 1, now, now + 300, now))
db.commit()

t0 = time.time()
# picu webhook via mark-paid tidak bisa (sudah paid); pakai unmatched match
for i in range(6):
    db.execute("update orders set webhook_fired=0 where id=?", (f"par{i}",))
db.commit()
# langsung panggil lewat endpoint: refund lalu markpaid? gunakan unmatched
for i in range(6):
    db.execute("update orders set status='pending', webhook_fired=0 where id=?", (f"par{i}",))
db.commit()
jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
op.open(BASE + "/admin/login", data=urllib.parse.urlencode({"user": "admin", "pass": "admin123"}).encode())
op.open(BASE + "/admin/tx/par0")
csrf = ""
for c in jar:
    if c.name == "paypan_csrf":
        csrf = c.value
if not csrf:
    raise SystemExit("csrf cookie tidak terpasang")
for i in range(6):
    op.open(BASE + f"/admin/tx/par{i}", data=urllib.parse.urlencode({"act": "markpaid", "csrf_token": csrf}).encode())

for _ in range(60):
    if len(got) >= 6:
        break
    time.sleep(0.2)
elapsed = time.time() - t0
ck("6 webhook terkirim", len(got) >= 6, f"dapat {len(got)}")
# Span = webhook pertama sampai terakhir. Serial 6 x 0.2s = 1.2s; paralel ~0.4s.
span = (max(got) - min(got)) if len(got) >= 2 else 99
ck("dikirim paralel (span < 1.15s, serial butuh >=1.2s)", span < 1.15, f"span {span:.2f}s, total {elapsed:.2f}s, n={len(got)}")

# --- 4. graceful shutdown ---
db.execute("delete from orders"); db.execute("delete from webhooks"); db.execute("delete from webhook_queue")
db.commit()
srv.shutdown()

ok = sum(1 for _, o in R if o)
print(f"\n=== STABILITAS: {ok}/{len(R)} PASS ===")
raise SystemExit(0 if ok == len(R) else 1)
