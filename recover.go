package main

import (
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

func sleepSec(n int) { time.Sleep(time.Duration(n) * time.Second) }

// recoverMW: satu panic di handler tidak boleh mematikan seluruh server.
// Tanpa ini, satu nil dereference = semua request mati + restart-loop.
func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				// best effort: balas 500 kalau header belum terkirim
				defer func() { _ = recover() }()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"ok":false,"error":"internal error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// safeGo: jalankan goroutine worker dengan recover + restart otomatis.
// Worker yang panic tidak boleh mati diam-diam (mis. expireWorker mati =
// kode tidak pernah kembali ke pool).
func safeGo(name string, fn func()) {
	go func() {
		for {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("WORKER PANIC [%s]: %v\n%s", name, rec, debug.Stack())
					}
				}()
				fn()
			}()
			// fn() kembali normal (tidak seharusnya untuk loop worker) — jeda lalu ulang
			log.Printf("WORKER [%s] berhenti, restart dalam 5 detik", name)
			sleepSec(5)
		}
	}()
}
