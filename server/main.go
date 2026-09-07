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
	if *tok == "" {
		log.Fatal("token wajib: -token atau PAYPAN_TOKEN")
	}

	db, err := initDB(*dbPath)
	if err != nil {
		log.Fatal(err)
	}

	// template QRIS statis: raw text payload, dibaca sekali saat start
	qrisBytes, err := os.ReadFile("qris_base.txt")
	if err != nil {
		log.Fatal("qris_base.txt wajib ada di folder kerja server (raw text QRIS statis lo): ", err)
	}
	qrisBase := strings.TrimSpace(string(qrisBytes))
	if !strings.Contains(qrisBase, "010211") || !strings.Contains(qrisBase, "5802ID") {
		log.Fatal("payload qris_base.txt tidak dikenali (butuh tag 010211 + 5802ID)")
	}

	s := &srv{
		db:      db,
		qris:    qrisBase,
		token:   *tok,
		tgToken: *tgToken,
		tgChat:  *tgChat,
		httpc:   &http.Client{Timeout: 10 * time.Second},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/notif", s.handleNotif)
	mux.HandleFunc("/api/order", s.handleOrderCreate)
	mux.HandleFunc("/api/order/", s.handleOrderStatus)
	mux.HandleFunc("/pay/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/qr.png") {
			s.handleQR(w, r)
		} else {
			s.handleCheckout(w, r)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("Paypan payment gateway OK\n"))
	})

	go s.expireWorker()
	log.Printf("Paypan server listening %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
