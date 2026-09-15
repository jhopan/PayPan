#!/bin/bash
# watch cloudflared log selagi test request
ssh laptop-debian 'timeout 15 journalctl -u cloudflared-paypan -f --no-pager' > /tmp/cflog.txt 2>&1 &
TPID=$!
sleep 2
TOKEN="pp_WqgO5UV76NdrpECc"
echo "=== request via domain:"
curl -s -X POST https://paypan.jhopan.my.id/api/invoice -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"price":46000}' | head -c 100
echo
sleep 10
kill $TPID 2>/dev/null
wait $TPID 2>/dev/null
echo "=== cloudflared debian log:"
tail -6 /tmp/cflog.txt
echo "=== cek DB debian:"
ssh laptop-debian 'python3 - <<PYEOF
import sqlite3, time
db = sqlite3.connect("/var/lib/paypan/paypan.db")
now = int(time.time())
rows = db.execute("select id,status,total from orders where created_at > ?", (now-60,)).fetchall()
print("order 1 menit terakhir:", rows)
PYEOF'
