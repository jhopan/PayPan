package main

import "testing"

func TestPortFromAddr(t *testing.T) {
	cases := map[string]string{
		":9090":          "9090",
		"127.0.0.1:9090": "9090",
		"0.0.0.0:8080":   "8080",
		"[::]:9090":      "9090",
	}
	for in, want := range cases {
		if got := portFromAddr(in); got != want {
			t.Errorf("portFromAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanAddressesNoLoopback(t *testing.T) {
	for _, ip := range lanAddresses() {
		if ip == "127.0.0.1" || ip == "::1" {
			t.Fatalf("loopback tidak boleh muncul: %s", ip)
		}
	}
}
