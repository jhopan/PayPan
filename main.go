package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", envOr("PAYPAN_ADDR", ":8090"), "listen address")
	dbPath := flag.String("db", envOr("PAYPAN_DB", "paypan.db"), "sqlite path")
	tok := flag.String("token", "", "bearer token utk /api/notif dan /api/order")
	tgToken := flag.String("tgtoken", "", "telegram bot token (opsional)")
	tgChat := flag.String("tgchat", "", "telegram chat id (opsional)")
	flag.Parse()

	LoadTuningFromEnv()

	// env mengisi default; flag eksplisit tetap menang (flag.Parse menimpa
	// hanya jika user mengetik flag — jadi urutan: default env < flag).
	// Untuk token: flag > env (seed master saja; auth asli = tabel apps).
	if *tok == "" {
		*tok = os.Getenv("PAYPAN_TOKEN")
	}
	if *tgToken == "" {
		*tgToken = os.Getenv("PAYPAN_TGTOKEN")
	}
	if *tgChat == "" {
		*tgChat = os.Getenv("PAYPAN_TGCHAT")
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
		dbPath:  *dbPath,
		addr:    *addr,
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
	LoadTuningFromDB(db)
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
	mux.HandleFunc("/admin/kasir", s.requireAdminCSRF(s.handleKasir))
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
		// CSRF: form kasir kirim header X-CSRF-Token (fetch, bukan form biasa)
		if r.Method == http.MethodPost {
			ck, _ := r.Cookie(csrfCookie)
			hdr := r.Header.Get("X-CSRF-Token")
			if ck == nil || subtle.ConstantTimeCompare([]byte(hdr), []byte(ck.Value)) != 1 {
				s.writeJSON(w, 403, map[string]string{"error": "csrf"})
				return
			}
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
	mux.HandleFunc("/static/", s.staticHandler)
	mux.HandleFunc("/favicon.ico", s.faviconHandler)
	mux.HandleFunc("/admin/login", s.handleLogin)
	mux.HandleFunc("/admin/logout", s.handleLogout)
	mux.HandleFunc("/admin/apps", s.requireAdminCSRF(s.handleAdminApps))
	mux.HandleFunc("/admin/config", s.requireAdminCSRF(s.handleAdminConfig))
	mux.HandleFunc("/admin/log", s.requireAdminCSRF(s.handleAdminLog))
	mux.HandleFunc("/admin/laporan", s.requireAdminCSRF(s.handleAdminLaporan))
	mux.HandleFunc("/admin/backup", s.requireAdminCSRF(s.handleAdminBackup))
	mux.HandleFunc("/admin/export", s.handleExport)
	mux.HandleFunc("/admin/tx/", s.requireAdminCSRF(s.handleTxDetail))
	mux.HandleFunc("/admin", s.requireAdminCSRF(s.handleAdminHome))
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", 302)
	})
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("Paypan payment gateway OK\n"))
	})

	// semua worker dibungkus safeGo: panic tidak mematikan worker permanen
	safeGo("retention", s.retentionWorker)
	safeGo("webhook", s.webhookWorker)
	safeGo("expire", s.expireWorker)
	safeGo("backup", func() { s.backupWorker(*dbPath) })
	safeGo("apijanitor", apiJanitorWorker)
	safeGo("sheets", s.sheetWorker)

	// http.Server dengan timeout: tahan koneksi lambat (slowloris) & header raksasa
	srv := &http.Server{
		Addr:              *addr,
		Handler:           secureHeaders(recoverMW(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// graceful shutdown: SIGTERM/SIGINT -> tunggu request aktif selesai
	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("sinyal berhenti diterima — menunggu request aktif (maks 15s)")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Println("shutdown paksa:", err)
		}
		close(done)
	}()

	log.Printf("Paypan server listening %s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-done
	log.Println("server berhenti bersih")
}

// envOr: baca env, fallback default. Env tidak pernah menimpa flag eksplisit.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}