package main

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	h := hashPassword("rahasia123")
	if h == "" {
		t.Fatal("hash kosong")
	}
	if !isHashedPassword(h) {
		t.Fatalf("format hash salah: %s", h)
	}
	if !verifyPassword("rahasia123", h) {
		t.Fatal("password benar harus lolos")
	}
	if verifyPassword("salah", h) {
		t.Fatal("password salah tidak boleh lolos")
	}
}

func TestHashPasswordUniqueSalt(t *testing.T) {
	a := hashPassword("sama")
	b := hashPassword("sama")
	if a == b {
		t.Fatal("dua hash password sama harus berbeda (salt acak)")
	}
	if !verifyPassword("sama", a) || !verifyPassword("sama", b) {
		t.Fatal("kedua hash harus valid untuk password sama")
	}
}

func TestHashNotContainPlaintext(t *testing.T) {
	pw := "janganterbaca"
	h := hashPassword(pw)
	if strings.Contains(h, pw) {
		t.Fatalf("hash tidak boleh memuat plaintext: %s", h)
	}
}

func TestIsHashedPasswordRejectsPlaintext(t *testing.T) {
	for _, v := range []string{"admin123", "", "pbkdf2$x$y", "pbkdf2$210000$c2FsdA$aGFzaA"} {
		if v == "pbkdf2$210000$c2FsdA$aGFzaA" {
			continue // format benar
		}
		if isHashedPassword(v) {
			t.Errorf("%q tidak boleh dianggap hash", v)
		}
	}
	if !isHashedPassword("pbkdf2$210000$c2FsdA$aGFzaA") {
		t.Error("format hash valid harus dikenali")
	}
}

func TestCSRFTokenUniqueAndLong(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok := newCSRFToken()
		if len(tok) < 40 {
			t.Fatalf("token terlalu pendek: %d", len(tok))
		}
		if seen[tok] {
			t.Fatal("token CSRF duplikat")
		}
		seen[tok] = true
	}
}

func TestPortFromAddrVariants(t *testing.T) {
	cases := map[string]string{
		":9090":          "9090",
		"127.0.0.1:9090": "9090",
		"[::]:9090":      "9090",
		"0.0.0.0:80":     "80",
	}
	for in, want := range cases {
		if got := portFromAddr(in); got != want {
			t.Errorf("portFromAddr(%q)=%q want %q", in, got, want)
		}
	}
}
