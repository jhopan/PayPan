package main

import (
	"net"
	"sort"
	"strings"
)

// lanAddresses: daftar IPv4 privat (LAN) milik mesin ini, tanpa loopback.
// Diurutkan: 192.168.x dulu (paling umum untuk WiFi rumah), lalu 10.x, lalu 172.16-31.x.
func lanAddresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() || seen[ip4.String()] {
				continue
			}
			// 172.17.x = bridge Docker/WSL, bukan LAN rumah — jangan ditawarkan
			if strings.HasPrefix(ip4.String(), "172.17.") {
				continue
			}
			seen[ip4.String()] = true
			out = append(out, ip4.String())
		}
	}
	rank := func(ip string) int {
		switch {
		case strings.HasPrefix(ip, "192.168."):
			return 0
		case strings.HasPrefix(ip, "10."):
			return 1
		default:
			return 2
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// portFromAddr: ":9090" -> "9090", "127.0.0.1:9090" -> "9090".
func portFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return "9090"
}
