#!/usr/bin/env bash
# ============================================================
#  PayPan Server — installer & control menu
#  https://github.com/jhopan/PayPan
#
#  Pakai:
#    curl -fsSL https://raw.githubusercontent.com/jhopan/PayPan/master/install.sh | bash
#  atau:
#    bash install.sh
#
#  Yang dilakukan:
#   - cek OS/arch, cek & install dependencies (curl, tar, jq — via apt/dnf/yum)
#   - download binary SIAP PAKAI dari GitHub Releases (bukan build dari source)
#   - setup folder /opt/paypan + data /var/lib/paypan
#   - install menu `paypan` untuk start/stop/restart/status/log/update/uninstall
#   - pilihan tunnel: cloudflared / ngrok / caddy / nginx / tanpa tunnel
# ============================================================
set -euo pipefail

REPO="jhopan/PayPan"
INSTALL_DIR="/opt/paypan"
DATA_DIR="/var/lib/paypan"
BIN="$INSTALL_DIR/paypan"
SERVICE="paypan"
VERSION="${PAYPAN_VERSION:-1.1.1}"

# ---------- helper ----------
R="\033[0m"; G="\033[1;32m"; Y="\033[1;33m"; RED="\033[1;31m"; C="\033[1;36m"
say()  { echo -e "${G}==>${R} $*"; }
warn() { echo -e "${Y}!>${R} $*"; }
die()  { echo -e "${RED}x>${R} $*"; exit 1; }

need_root() {
  if [[ $EUID -ne 0 ]]; then
    die "Jalankan sebagai root (sudo bash install.sh). Folder $INSTALL_DIR & systemd butuh root."
  fi
}

pkg_install() {
  # $@ = daftar paket
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq && apt-get install -y -qq "$@"
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y -q "$@"
  elif command -v yum >/dev/null 2>&1; then
    yum install -y -q "$@"
  elif command -v pacman >/dev/null 2>&1; then
    pacman -Sy --noconfirm "$@"
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-progress "$@"
  else
    die "Package manager gak dikenal. Install manual: $*"
  fi
}

ensure_deps() {
  say "Cek dependencies..."
  local missing=()
  for c in curl tar jq; do
    command -v "$c" >/dev/null 2>&1 || missing+=("$c")
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    say "Install: ${missing[*]}"
    pkg_install "${missing[@]}"
  fi
  say "Dependencies OK (curl, tar, jq)"
}

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "Arch $arch belum didukung (yang rilis: amd64)" ;;
  esac
  [[ "$os" == "linux" ]] || die "Installer ini untuk Linux. Windows/macOS: build manual atau download dari Releases."
  echo "linux-${arch}"
}

download_release() {
  local platform="$1"
  local url="https://github.com/${REPO}/releases/download/v${VERSION}/paypan-linux-${platform##*-}.tar.gz"
  say "Download Paypan v${VERSION} dari GitHub Releases..."
  say "  $url"
  mkdir -p "$INSTALL_DIR" "$DATA_DIR"
  curl -fsSL "$url" | tar xz -C "$INSTALL_DIR"
  mv "$INSTALL_DIR/paypan-linux-${platform##*-}" "$BIN"
  chmod +x "$BIN"
  say "Binary terpasang: $BIN"
}

# ---------- systemd ----------
write_service() {
  say "Pasang systemd service..."
  cat > "/etc/systemd/system/${SERVICE}.service" <<EOF
[Unit]
Description=Paypan payment gateway (QRIS)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=${BIN} -addr 127.0.0.1:${PORT:-9090} -db ${DATA_DIR}/paypan.db
WorkingDirectory=${INSTALL_DIR}
Restart=always
RestartSec=5
User=root
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "${SERVICE}" >/dev/null 2>&1
}

setup_qris() {
  if [[ -s "${DATA_DIR}/qris_base.txt" ]]; then
    say "qris_base.txt sudah ada, skip."
    return
  fi
  echo
  warn "Paypan butuh payload QRIS statis lo (raw text QRIS)."
  warn "Bisa diisi sekarang ATAU nanti via web admin (menu Konfigurasi)."
  read -r -p "Isi sekarang? [y/N] " yn
  if [[ "$yn" =~ ^[Yy]$ ]]; then
    read -r -p "Paste payload QRIS statis: " payload
    if [[ "$payload" =~ ^0002 ]]; then
      printf '%s' "$payload" > "${DATA_DIR}/qris_base.txt"
      say "qris_base.txt tersimpan."
    else
      warn "Payload tidak dimulai dengan 0002 — simpan manual nanti via admin."
    fi
  else
    say "Di-skip. Isi via web admin → Konfigurasi nanti."
  fi
}

# ---------- tunnel ----------
setup_tunnel() {
  echo
  say "Pilih akses publik:"
  echo -e "  ${C}1${R}) Cloudflare Tunnel  (rekomendasi: domain sendiri + HTTPS, gratis)"
  echo -e "  ${C}2${R}) ngrok              (URL instan acak, enak buat coba-coba)"
  echo -e "  ${C}3${R}) Caddy              (reverse proxy + HTTPS otomatis, butuh domain)"
  echo -e "  ${C}4${R}) Nginx              (reverse proxy manual, butuh domain + cert)"
  echo -e "  ${C}5${R}) Tanpa tunnel       (LAN saja / sudah ada proxy sendiri)"
  read -r -p "Pilihan [1-5]: " pilihan

  case "$pilihan" in
    1) setup_cloudflared ;;
    2) setup_ngrok ;;
    3) setup_caddy ;;
    4) setup_nginx ;;
    *) say "Tanpa tunnel. Server jalan di 127.0.0.1:${PORT:-9090}." ;;
  esac
}

setup_cloudflared() {
  say "Install cloudflared..."
  if ! command -v cloudflared >/dev/null 2>&1; then
    pkg_install debian-keyring debian-archive-keyring apt-transport-https ca-certificates || true
    curl -fsSL -o /usr/local/bin/cloudflared \
      "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64" \
      && chmod +x /usr/local/bin/cloudflared
  fi
  say "cloudflared terpasang: $(cloudflared --version 2>/dev/null || echo '?')"
  cat > "/etc/systemd/system/${SERVICE}-tunnel.service" <<EOF
[Unit]
Description=Cloudflare Tunnel for Paypan
After=network-online.target ${SERVICE}.service

[Service]
# mode quick tunnel: URL acak instan. Untuk domain sendiri:
#   cloudflared tunnel login && cloudflared tunnel create paypan
# lalu ganti ExecStart sesuai token/credentials yang dikasih CF dashboard.
ExecStart=/usr/local/bin/cloudflared tunnel --url http://127.0.0.1:${PORT:-9090} --no-autoupdate
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now "${SERVICE}-tunnel" >/dev/null 2>&1
  sleep 3
  local url
  url="$(journalctl -u "${SERVICE}-tunnel" --no-pager -n 50 2>/dev/null | grep -oP 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 || true)"
  if [[ -n "$url" ]]; then
    say "Quick tunnel aktif: ${C}${url}${R}"
    warn "URL ini acak & berubah saat restart tunnel. Untuk domain tetap: pakai CF dashboard (remote-managed tunnel)."
  else
    warn "URL tunnel belum ketemu di log. Cek: journalctl -u ${SERVICE}-tunnel -f"
  fi
  warn "Masukkan URL tunnel + token di web admin / app HP nanti."
}

setup_ngrok() {
  say "Install ngrok..."
  if ! command -v ngrok >/dev/null 2>&1; then
    pkg_install snapd 2>/dev/null && snap install ngrok 2>/dev/null || {
      curl -fsSL -o /tmp/ngrok.tgz "https://bin.equinox.io/c/bNyj1mQVY4c/ngrok-v3-stable-linux-amd64.tgz" \
        && tar xzf /tmp/ngrok.tgz -C /usr/local/bin && rm -f /tmp/ngrok.tgz
    }
  fi
  warn "ngrok butuh authtoken (gratis, daftar di dashboard ngrok)."
  read -r -p "Paste authtoken (kosong = skip): " tok
  [[ -n "$tok" ]] && ngrok config add-authtoken "$tok"
  cat > "/etc/systemd/system/${SERVICE}-tunnel.service" <<EOF
[Unit]
Description=ngrok tunnel for Paypan
After=${SERVICE}.service

[Service]
ExecStart=/usr/local/bin/ngrok http 127.0.0.1:${PORT:-9090} --log stdout
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now "${SERVICE}-tunnel" >/dev/null 2>&1 || true
  warn "URL ngrok: cek http://127.0.0.1:4040/api/tunnels (atau dashboard ngrok)."
}

setup_caddy() {
  say "Install Caddy..."
  if ! command -v caddy >/dev/null 2>&1; then
    pkg_install debian-keyring debian-archive-keyring apt-transport-https 2>/dev/null || true
    curl -1sLf "https://dl.cloudsmith.io/public/caddy/stable/gpg.key" | gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null || true
    curl -1sLf "https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt" | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null 2>&1 || true
    pkg_install caddy || die "Gagal install caddy — install manual lalu jalankan lagi."
  fi
  read -r -p "Domain untuk Paypan (mis. pay.example.com): " domain
  [[ -z "$domain" ]] && domain="localhost"
  cat > "/etc/caddy/Caddyfile" <<EOF
${domain} {
    reverse_proxy 127.0.0.1:${PORT:-9090}
}
EOF
  systemctl enable --now caddy >/dev/null 2>&1 || true
  systemctl reload caddy 2>/dev/null || true
  say "Caddy reverse proxy: https://${domain} → 127.0.0.1:${PORT:-9090}"
  warn "Pastikan DNS domain mengarah ke VPS ini (A/AAAA record)."
}

setup_nginx() {
  say "Install nginx..."
  command -v nginx >/dev/null 2>&1 || pkg_install nginx
  read -r -p "Domain untuk Paypan (mis. pay.example.com): " domain
  [[ -z "$domain" ]] && domain="_"
  cat > "/etc/nginx/sites-available/paypan" <<EOF
server {
    listen 80;
    server_name ${domain};

    location / {
        proxy_pass http://127.0.0.1:${PORT:-9090};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
  ln -sf /etc/nginx/sites-available/paypan /etc/nginx/sites-enabled/paypan 2>/dev/null || true
  nginx -t 2>/dev/null && systemctl reload nginx || systemctl restart nginx
  say "Nginx reverse proxy: http://${domain} → 127.0.0.1:${PORT:-9090}"
  warn "HTTPS: pasang certbot (pkg_install certbot python3-certbot-nginx && certbot --nginx -d ${domain})."
}

# ---------- menu ----------
# patch_menu_entry <file>: ubah salinan installer supaya `paypan` (tanpa argumen)
# langsung buka menu, bukan jalanin installer lagi. Murni sed (no python dependency).
patch_menu_entry() {
  local f="$1"
  sed -i 's|^main "\$@"$|if [[ $# -eq 0 ]]; then menu; else main "$@"; fi|' "$f"
  grep -q 'if \[\[ \$# -eq 0 \]\]; then menu' "$f" && say "Menu auto-aktif: ketik ${C}paypan${R} di terminal mana pun." || warn "patch menu gagal (jalankan manual: paypan install)"
}

menu() {
  while true; do
    echo
    echo -e "${C}╔══════════════════════════════════════╗${R}"
    echo -e "${C}║   PayPan Server — Kontrol            ║${R}"
    echo -e "${C}╚══════════════════════════════════════╝${R}"
    local status="STOPPED"
    systemctl is-active --quiet "${SERVICE}" && status="RUNNING"
    local tunnel_status="OFF"
    systemctl is-active --quiet "${SERVICE}-tunnel" 2>/dev/null && tunnel_status="ON"
    echo -e "  Status server : ${status}   |  Tunnel: ${tunnel_status}"
    echo
    echo "   1) Start           5) Log live (journalctl -f)"
    echo "   2) Stop            6) Status detail (systemctl status)"
    echo "   3) Restart         7) Tunnel: start/stop/restart"
    echo "   4) Update binary   8) Uninstall"
    echo "   q) Keluar"
    echo
    read -r -p "Pilih: " m
    case "$m" in
      1) systemctl start "${SERVICE}" && say "started." ;;
      2) systemctl stop "${SERVICE}" && say "stopped." ;;
      3) systemctl restart "${SERVICE}" && say "restarted." ;;
      4) update_binary ;;
      5) journalctl -u "${SERVICE}" -f --no-pager ;;
      6) systemctl status "${SERVICE}" --no-pager -l ;;
      7) tunnel_menu ;;
      8) uninstall ;;
      q|Q) exit 0 ;;
      *) warn "pilihan salah" ;;
    esac
  done
}

tunnel_menu() {
  read -r -p "Tunnel action [start/stop/restart/status]: " t
  case "$t" in
    start)   systemctl start "${SERVICE}-tunnel" 2>/dev/null && say "tunnel started." || warn "tunnel belum terpasang (install ulang, pilih tunnel)." ;;
    stop)    systemctl stop "${SERVICE}-tunnel" 2>/dev/null && say "tunnel stopped." ;;
    restart) systemctl restart "${SERVICE}-tunnel" 2>/dev/null && say "tunnel restarted." ;;
    status)  systemctl status "${SERVICE}-tunnel" --no-pager -l 2>/dev/null || warn "tunnel belum terpasang." ;;
    *) warn "?" ;;
  esac
}

update_binary() {
  say "Update binary ke v${VERSION}..."
  systemctl stop "${SERVICE}" 2>/dev/null || true
  download_release "$(detect_platform)"
  systemctl start "${SERVICE}"
  say "Update selesai."
}

uninstall() {
  warn "Ini akan menghapus binary + service (DATA DB TETAP di ${DATA_DIR})."
  read -r -p "Yakin? ketik HAPUS: " c
  [[ "$c" == "HAPUS" ]] || { say "dibatalkan."; return; }
  systemctl disable --now "${SERVICE}" "${SERVICE}-tunnel" 2>/dev/null || true
  rm -f "/etc/systemd/system/${SERVICE}.service" "/etc/systemd/system/${SERVICE}-tunnel.service"
  systemctl daemon-reload
  rm -rf "$INSTALL_DIR"
  rm -f /usr/local/bin/paypan
  say "Uninstalled. Data di ${DATA_DIR} gak disentuh (hapus manual kalau mau)."
}

# ---------- main ----------
main() {
  echo -e "${C}PayPan installer v${VERSION}${R}"
  need_root
  ensure_deps
  local platform
  platform="$(detect_platform)"

  if [[ -x "$BIN" ]]; then
    say "Paypan sudah terpasang di $BIN."
    read -r -p "Buka menu kontrol? [Y/n] " yn
    [[ ! "$yn" =~ ^[Nn]$ ]] && menu
    return
  fi

  read -r -p "Port server [9090]: " port_input
  PORT="${port_input:-9090}"

  download_release "$platform"
  write_service
  setup_qris
  systemctl start "${SERVICE}"
  sleep 2
  systemctl is-active --quiet "${SERVICE}" && say "Paypan jalan: http://127.0.0.1:${PORT}" || die "Server gagal start — cek: journalctl -u ${SERVICE}"
  setup_tunnel

  # shortcut `paypan` di PATH: salin installer ke /usr/local/bin (bukan symlink ke
  # file sementara curl|bash), lalu sedikit patch supaya langsung buka menu
  install -m 755 "$0" /usr/local/bin/paypan
  patch_menu_entry /usr/local/bin/paypan

  echo
  say -e "Selesai! Web admin: ${C}http://localhost:${PORT}/admin${R} (default admin/admin123 — SEGERA GANTI)"
  say "Menu kontrol kapan saja: ketik ${C}paypan${R} di terminal mana pun (auto-buka menu)"
  say "Data DB: ${DATA_DIR}/paypan.db  |  Binary: ${BIN}"
}

main "$@"
