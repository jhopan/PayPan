package main

import (
	"database/sql"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestClaimOrderUniqueness(t *testing.T) {
	db := newTestDB(t)
	s := &srv{db: db}
	now := time.Now().Unix()

	oid, code, total, exp, err := s.claimOrder(1000, 0, "", "")
	if err != nil {
		t.Fatalf("claimOrder: %v", err)
	}
	if oid == "" || code < 1 || code > 999 {
		t.Fatalf("hasil claim tidak valid: id=%q code=%d", oid, code)
	}
	if total != 1000+int64(code) {
		t.Fatalf("total %d != price+code %d", total, 1000+int64(code))
	}
	if exp <= now {
		t.Fatalf("expiry harus di masa depan: %d <= %d", exp, now)
	}

	// order kedua di level sama harus dapat kode berbeda
	_, code2, _, _, err := s.claimOrder(1000, 0, "", "")
	if err != nil {
		t.Fatalf("claimOrder kedua: %v", err)
	}
	if code2 == code {
		t.Fatalf("kode duplikat untuk price sama: %d", code2)
	}
}

func TestClaimOrderParallelNoDuplicate(t *testing.T) {
	db := newTestDB(t)
	s := &srv{db: db}

	const n = 10
	var wg sync.WaitGroup
	totals := make([]int64, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, total, _, err := s.claimOrder(1000, 0, "", "")
			totals[i], errs[i] = total, err
		}(i)
	}
	wg.Wait()

	seen := map[int64]bool{}
	ok := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			continue
		}
		ok++
		if seen[totals[i]] {
			t.Fatalf("total duplikat dari klaim paralel: %d", totals[i])
		}
		seen[totals[i]] = true
	}
	if ok == 0 {
		t.Fatal("tidak ada klaim sukses sama sekali")
	}
	t.Logf("%d/%d klaim sukses, semua total unik", ok, n)
}

func TestExpiredCodeCooldown(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	// order expired baru saja (dalam cooldown 2 jam)
	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at) VALUES('e1',1000,1,1001,'expired',?,?)", now-300, now-10)
	if !codeUsedInPrice(db, 1000, 1) {
		t.Fatal("kode expired dalam cooldown harus TERPAKAI")
	}
	if !totalClaimed(db, 1001) {
		t.Fatal("total expired dalam cooldown harus TERPAKAI")
	}

	// order expired 3 jam lalu (lewat cooldown 2 jam)
	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at) VALUES('e2',1000,2,1002,'expired',?,?)", now-3*3600, now-3*3600)
	if codeUsedInPrice(db, 1000, 2) {
		t.Fatal("kode expired lewat cooldown harus BEBAS")
	}
	if totalClaimed(db, 1002) {
		t.Fatal("total expired lewat cooldown harus BEBAS")
	}
}

func TestPaidCodeCooldown(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()

	// paid 1 jam lalu -> masih terpakai (cooldown 24 jam)
	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at,paid_at) VALUES('p1',2000,5,2005,'paid',?,?,?)", now-4000, now-3700, now-3600)
	if !codeUsedInPrice(db, 2000, 5) {
		t.Fatal("kode paid 1 jam lalu harus TERPAKAI (cooldown 24 jam)")
	}

	// paid 25 jam lalu -> bebas
	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at,paid_at) VALUES('p2',2000,6,2006,'paid',?,?,?)", now-25*3600, now-25*3600, now-25*3600)
	if codeUsedInPrice(db, 2000, 6) {
		t.Fatal("kode paid 25 jam lalu harus BEBAS")
	}
	if totalClaimed(db, 2006) {
		t.Fatal("total paid 25 jam lalu harus BEBAS")
	}
}

func TestPaidCooldownConfigurable(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	old := tuning.PaidCooldown
	defer func() { tuning.PaidCooldown = old }()

	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at,paid_at) VALUES('p3',3000,9,3009,'paid',?,?,?)", now-150, now-150, now-150)

	tuning.PaidCooldown = 86400
	if !codeUsedInPrice(db, 3000, 9) {
		t.Fatal("dengan cooldown 24 jam, paid 150s harus TERPAKAI")
	}

	tuning.PaidCooldown = 60
	if codeUsedInPrice(db, 3000, 9) {
		t.Fatal("dengan cooldown 60s, paid 150s harus BEBAS")
	}
}

func TestPendingBlocksTotal(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at) VALUES('x1',5000,3,5003,'pending',?,?)", now, now+300)
	if !totalClaimed(db, 5003) {
		t.Fatal("total pending harus TERPAKAI")
	}
	if !codeUsedInPrice(db, 5000, 3) {
		t.Fatal("kode pending harus TERPAKAI")
	}
}

func TestMatchPendingOnlyFresh(t *testing.T) {
	db := newTestDB(t)
	s := &srv{db: db}
	now := time.Now().Unix()

	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at) VALUES('m1',1000,1,1001,'pending',?,?)", now-100, now+200)
	oid, err := s.matchPending(1001, now)
	if err != nil || oid != "m1" {
		t.Fatalf("matchPending fresh: oid=%q err=%v", oid, err)
	}

	// order pending yang sudah lewat expiry TIDAK boleh match
	db.Exec("INSERT INTO orders(id,price,code,total,status,created_at,expires_at) VALUES('m2',1000,2,1002,'pending',?,?)", now-400, now-100)
	if _, err := s.matchPending(1002, now); err != sql.ErrNoRows {
		t.Fatalf("order lewat expiry tidak boleh match, err=%v", err)
	}
}

func TestTuningSetAndPersist(t *testing.T) {
	db := newTestDB(t)
	SetTuning(db, "tuning_invoice_expiry", 120)
	if tuning.InvoiceExpirySec() != 120 {
		t.Fatalf("memori tidak update: %d", tuning.InvoiceExpirySec())
	}
	var v string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='tuning_invoice_expiry'").Scan(&v); err != nil {
		t.Fatalf("DB tidak tersimpan: %v", err)
	}
	if v != "120" {
		t.Fatalf("nilai DB %q, want 120", v)
	}
	// reset ke default supaya test lain tidak terpengaruh
	SetTuning(db, "tuning_invoice_expiry", 300)
	_ = strconv.Itoa(0)
}
