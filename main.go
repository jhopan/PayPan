package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	dbPath := flag.String("db", "paypan.db", "sqlite path")
	tok := flag.String("token", "", "bearer token utk /api/notif dan /api/order")
	tgToken := flag.String("tgtoken", "", "telegram bot token (opsional)")
	tgChat := flag.String("tgchat", "", "telegram chat id (opsional)")
	flag.Parse()

	if *tok == "" {
		*tok = os.Getenv("PAYPAN_TOKEN")
	}
	// token dari flag/env hanya dipakai sebagai token "master" awal;
	// auth sesungguhnya = tabel apps (dikelola via web admin)

	db, err := initDB(*dbPath)
	if err != nil {
		log.Fatal(err)
	}

	ensureAdminDefaults(db)
	_ = db

	// QRIS integrity: butuh srv method yang pakai db — inisialisasi srv minimal dulu
	s := &srv{
		db:      db,
		token:   *tok,
		tgToken: *tgToken,
		tgChat:  *tgChat,
		httpc:   &http.Client{Timeout: 10 * time.Second},
	}

	// QRIS hash pinning: verifikasi integritas payload saat startup
	qrisBase, qrisOK := s.verifyQrisIntegrity()
	if !qrisOK {
		log.Println("PERINGATAN: qris_base.txt berubah tanpa melalui admin — invoice DINONAKTIFKAN")
		s.alertQrisTamper()
	}
	s.qris = qrisBase

	// token/chat Telegram dari web (settings) menimpa flag — selalu DB yang menang
	s.loadTGFromDB()
	_ = s.token // legacy: auth lewat authScope (tabel apps)

	mux := http.NewServeMux()
	// API (auth per-app token)
	mux.HandleFunc("/api/notif", s.handleNotif)
	mux.HandleFunc("/api/order", s.handleOrderCreate)
	mux.HandleFunc("/api/order/", s.handleOrderStatus)
	// checkout publik
	mux.HandleFunc("/pay/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/qr.png") {
			s.handleQR(w, r)
		} else {
			s.handleCheckout(w, r)
		}
	})
	// kasir: bagian dari /admin/*
	mux.HandleFunc("/admin/kasir", s.handleKasir)
	// API invoice terstandarisasi (untuk bot/website/integrasi)
	mux.HandleFunc("/api/invoice", s.handleInvoiceCreate)
	mux.HandleFunc("/api/invoice/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/api/invoice/")
		switch {
		case strings.HasSuffix(p, "/cancel"):
			r.URL.Path = "/api/invoice/" + strings.TrimSuffix(p, "/cancel") + "/cancel"
			s.handleInvoiceCancel(w, r)
		case strings.HasSuffix(p, "/refund"):
			r.URL.Path = "/api/invoice/" + strings.TrimSuffix(p, "/refund") + "/refund"
			s.handleInvoiceRefund(w, r)
		default:
			s.handleInvoiceGet(w, r)
		}
	})
	mux.HandleFunc("/admin/kasir/order", func(w http.ResponseWriter, r *http.Request) {
		// endpoint order khusus kasir: auth = session admin, bukan token
		c, err := r.Cookie("paypan_session")
		if err != nil || !sessions.valid(c.Value) {
			s.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		// handleOrderCreate memeriksa scope token via authScope; untuk kasir
		// (session), buat order langsung tanpa token check.
		s.handleOrderCreateSession(w, r)
	})
	mux.HandleFunc("/admin/qr/", func(w http.ResponseWriter, r *http.Request) {
		// /admin/qr/{id}.png -> butuh session admin juga
		c, err := r.Cookie("paypan_session")
		if err != nil || !sessions.valid(c.Value) {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/admin/qr/")
		p = strings.TrimSuffix(p, ".png")
		r.URL.Path = "/pay/" + p + "/qr.png"
		s.handleQR(w, r)
	})
	// admin web
	mux.HandleFunc("/admin/login", s.handleLogin)
	mux.HandleFunc("/admin/logout", s.handleLogout)
	mux.HandleFunc("/admin/apps", s.handleAdminApps)
	mux.HandleFunc("/admin/config", s.handleAdminConfig)
	mux.HandleFunc("/admin/log", s.handleAdminLog)
	mux.HandleFunc("/admin/laporan", s.handleAdminLaporan)
	mux.HandleFunc("/admin/tx/", s.handleTxDetail)
	mux.HandleFunc("/admin", s.handleAdminHome)
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", 302)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("Paypan payment gateway OK\n"))
	})

	go s.expireWorker()
	go s.backupWorker(*dbPath)
	go apiJanitorWorker()
	log.Printf("Paypan server listening %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, secureHeaders(mux)))
}