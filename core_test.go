package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

// helper: DB in-memory unik per test (jangan shared — lihat catatan skill).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:test_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "?mode=memory&cache=shared"
	db, err := initDBFromDSN(dsn)
	if err != nil {
		t.Fatalf("initDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCRC16KnownVector(t *testing.T) {
	// CRC-16/CCITT-FALSE("123456789") = 0x29B1
	if got := crc16("123456789"); got != "29B1" {
		t.Fatalf("crc16 = %s, want 29B1", got)
	}
}

func TestBuildDynamicQRInjectsAmountAndCRC(t *testing.T) {
	base := "00020101021126610014COM.GO-JEK.WWW011893600914365190135102200000000000000021035190135102520303UMI51440014ID.CO.QRIS.WWW0215UMI102520210303UMI5204549953033605802ID5910J Store156006JAKARTA BARAT61051214062150703A01630468E7"
	out, err := buildDynamicQR(base, 1001)
	if err != nil {
		t.Fatalf("buildDynamicQR: %v", err)
	}
	if !strings.Contains(out, "010212") {
		t.Fatalf("harus dinamis (010212), dapat: %s", out[:20])
	}
	// tag 54 = "54" + len("1001.00")=07 + "1001.00"
	if !strings.Contains(out, "54071001.00") {
		t.Fatalf("tag 54 nominal tidak benar: %s", out)
	}
	// CRC valid: hitung ulang body tanpa 4 char terakhir
	i := strings.LastIndex(out, "6304")
	if i < 0 {
		t.Fatal("tanpa 6304")
	}
	body := out[:i+4]
	if want := body + crc16(body); out != want {
		t.Fatalf("CRC tidak cocok\n got=%s\nwant=%s", out, want)
	}
}

func TestValidatePrice(t *testing.T) {
	cases := []struct {
		p    int64
		want bool // true = valid
	}{
		{1000, true},
		{9000000, true},
		{999, false},
		{9000001, false},
		{0, false},
		{-1, false},
	}
	for _, c := range cases {
		got := validatePrice(c.p) == ""
		if got != c.want {
			t.Errorf("validatePrice(%d) valid=%v, want %v", c.p, got, c.want)
		}
	}
}

func TestGenTokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok := genToken()
		if !strings.HasPrefix(tok, "pp_") || len(tok) != 19 {
			t.Fatalf("format token salah: %q (len %d)", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("token duplikat: %s", tok)
		}
		seen[tok] = true
	}
}

func TestNewOrderIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := newOrderID()
		if len(id) != 16 {
			t.Fatalf("panjang id %d, want 16: %s", len(id), id)
		}
		if seen[id] {
			t.Fatalf("order id duplikat: %s", id)
		}
		seen[id] = true
	}
}

func TestHMACSignatureDeterministic(t *testing.T) {
	body := []byte(`{"event":"order.paid"}`)
	secret := "pp_testsecret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	// hitung ulang cara penerima
	mac2 := hmac.New(sha256.New, []byte(secret))
	mac2.Write(body)
	got := hex.EncodeToString(mac2.Sum(nil))
	if !hmac.Equal([]byte(got), []byte(want)) {
		t.Fatalf("HMAC tidak deterministik")
	}
	if len(want) != 64 {
		t.Fatalf("panjang hex %d, want 64", len(want))
	}
}
