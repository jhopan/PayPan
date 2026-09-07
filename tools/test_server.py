import http.server, json, sys

LOG = []

class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n).decode()
        auth = self.headers.get("Authorization", "")
        LOG.append((auth, body))
        print("RECV", auth, body, flush=True)
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def log_message(self, *a):
        pass

print("listening 0.0.0.0:8090", flush=True)
http.server.HTTPServer(("0.0.0.0", 8090), H).serve_forever()
