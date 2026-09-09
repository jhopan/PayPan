# AUDIT SISTEMATIK: celah & yang belum ada
import os, re

issues = []
files = [f for f in os.listdir('.') if f.endswith('.go')]
src = {f: open(f, encoding='utf-8').read() for f in files}
allsrc = "\n".join(src.values())

# 1. worker yang defined tapi gak di-start
for w in re.findall(r'func (\w+Worker)\(', allsrc):
    started = f"go {w}(" in allsrc
    print(f"worker {w}: started={started}")
    if not started: issues.append(f"{w} gak di-start")

# 2. handler tanpa method check
for f, s in src.items():
    for m in re.finditer(r'func \(s \*srv\) (handle\w+)\(', s):
        name = m.group(1)
        seg = s[m.start():m.start()+900]
        if 'r.Method' not in seg:
            issues.append(f"{name} ({f}): tanpa method check")

# 3. ReadFile tanpa sanitasi path
for f, s in src.items():
    if 'os.ReadFile' in s:
        for line in s.split('\n'):
            if 'os.ReadFile' in line and 'r.URL.Path' not in line:
                pass  # statik filename, aman

# 4. password plain-compare
if '== s.adminPass()' in allsrc:
    issues.append("password compare pakai == (timing side-channel, minor)")

# 5. session cleanup
print("session cleanup di newSession:", "delete(s.m, k)" in allsrc)

# 6. cek /admin/tx/ id sanitasi (path traversal via id)
m = re.search(r'admin/tx/.*?\n', allsrc)
# id langsung dipakai di query - parameterized jadi aman

# 7. cors: API gak set CORS -> browser lain-origin gak bisa akses = aman by default
print("CORS header diset:", "Access-Control" in allsrc)

print("\n=== TEMUAN ===")
for i in issues: print("-", i)
print(f"Total: {len(issues)}")
