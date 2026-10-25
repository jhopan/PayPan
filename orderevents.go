package main

import "time"

// logOrderEvent: catat perubahan status per order (riwayat transaksi).
// actor: "system" (worker/notif), nama admin, atau "app:<nama>".
func (s *srv) logOrderEvent(orderID, from, to, actor, note string) {
	if actor == "" {
		actor = "system"
	}
	s.db.Exec(`INSERT INTO order_events(order_id,from_status,to_status,actor,note,at)
		VALUES(?,?,?,?,?,?)`, orderID, from, to, actor, note, time.Now().Unix())
}

// orderEvent: satu baris riwayat untuk ditampilkan.
type orderEvent struct {
	From  string
	To    string
	Actor string
	Note  string
	At    int64
}

// orderEvents: riwayat status sebuah order, terbaru di atas.
func (s *srv) orderEvents(orderID string) []orderEvent {
	rows, err := s.db.Query(`SELECT from_status,to_status,actor,note,at
		FROM order_events WHERE order_id=? ORDER BY at DESC, id DESC LIMIT 50`, orderID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []orderEvent
	for rows.Next() {
		var e orderEvent
		if rows.Scan(&e.From, &e.To, &e.Actor, &e.Note, &e.At) == nil {
			out = append(out, e)
		}
	}
	return out
}

// setOrderStatus: ubah status + catat riwayat dalam satu langkah.
// Hanya mengubah kalau status saat ini = from (cegah transisi liar).
func (s *srv) setOrderStatus(orderID, from, to, actor, note string) bool {
	res, err := s.db.Exec("UPDATE orders SET status=? WHERE id=? AND status=?", to, orderID, from)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	s.logOrderEvent(orderID, from, to, actor, note)
	return true
}

// expireOrder: dipakai worker — tandai expired + riwayat.
func (s *srv) expireOrder(orderID string) {
	s.setOrderStatus(orderID, "pending", "expired", "system", "lewat window bayar")
}
