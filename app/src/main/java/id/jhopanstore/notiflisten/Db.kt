package id.jhopanstore.notiflisten

import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper

/**
 * Outbox SQLite. Semua notif masuk disimpan utuh SEBELUM diproses apa pun.
 * status: 0=pending, 1=sent, 2=failed
 */
class Db(context: Context) : SQLiteOpenHelper(context, "outbox.db", null, 1) {

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL(
            """CREATE TABLE notif(
                id TEXT PRIMARY KEY,
                pkg TEXT NOT NULL,
                title TEXT,
                text TEXT,
                amount INTEGER,
                source TEXT,
                created_at INTEGER NOT NULL,
                status INTEGER NOT NULL DEFAULT 0,
                tries INTEGER NOT NULL DEFAULT 0,
                last_error TEXT,
                sent_at INTEGER
            )"""
        )
        db.execSQL("CREATE INDEX idx_status ON notif(status, created_at)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldV: Int, newV: Int) {}

    fun insert(
        id: String, pkg: String, title: String?, text: String?,
        amount: Long?, source: String?, createdAt: Long
    ): Boolean {
        val st = writableDatabase.compileStatement(
            "INSERT OR IGNORE INTO notif(id,pkg,title,text,amount,source,created_at) VALUES(?,?,?,?,?,?,?)"
        )
        st.bindString(1, id)
        st.bindString(2, pkg)
        if (title != null) st.bindString(3, title) else st.bindNull(3)
        if (text != null) st.bindString(4, text) else st.bindNull(4)
        if (amount != null) st.bindLong(5, amount) else st.bindNull(5)
        if (source != null) st.bindString(6, source) else st.bindNull(6)
        st.bindLong(7, createdAt)
        return st.executeInsert() != -1L
    }

    fun pending(limit: Int): List<Row> {
        val out = ArrayList<Row>()
        // cap umur 48 jam: lebih tua dari itu dianggap batal (server expiry juga lewat)
        val cutoff = System.currentTimeMillis() - 48L * 3600_000
        writableDatabase.rawQuery(
            "SELECT id,pkg,title,text,amount,source,created_at,tries FROM notif WHERE status=0 AND tries<8 AND created_at>? ORDER BY created_at LIMIT ?",
            arrayOf(cutoff.toString(), limit.toString())
        ).use { c ->
            while (c.moveToNext()) {
                out.add(
                    Row(
                        c.getString(0), c.getString(1), c.getString(2), c.getString(3),
                        if (c.isNull(4)) null else c.getLong(4),
                        if (c.isNull(5)) null else c.getString(5),
                        c.getLong(6), c.getInt(7)
                    )
                )
            }
        }
        return out
    }

    fun markSent(id: String) =
        writableDatabase.execSQL(
            "UPDATE notif SET status=1, sent_at=? WHERE id=?",
            arrayOf(System.currentTimeMillis(), id)
        )

    fun markFailed(id: String, err: String?) {
        writableDatabase.execSQL(
            "UPDATE notif SET tries=tries+1, last_error=?, status=CASE WHEN tries+1>=8 THEN 2 ELSE 0 END WHERE id=?",
            arrayOf(err, id)
        )
    }

    fun counts(): Triple<Int, Int, Int> {
        var p = 0; var s = 0; var f = 0
        writableDatabase.rawQuery("SELECT status,COUNT(*) FROM notif GROUP BY status", null).use { c ->
            while (c.moveToNext()) {
                when (c.getInt(0)) {
                    0 -> p = c.getInt(1); 1 -> s = c.getInt(1); 2 -> f = c.getInt(1)
                }
            }
        }
        return Triple(p, s, f)
    }

    fun cleanup() {
        val cutoff = System.currentTimeMillis() - 7L * 24 * 3600_000
        writableDatabase.execSQL(
            "DELETE FROM notif WHERE status IN (1,2) AND created_at<?",
            arrayOf(cutoff)
        )
    }

    fun recent(limit: Int): List<Row> {
        val out = ArrayList<Row>()
        writableDatabase.rawQuery(
            "SELECT id,pkg,title,text,amount,source,created_at,tries FROM notif ORDER BY created_at DESC LIMIT ?",
            arrayOf(limit.toString())
        ).use { c ->
            while (c.moveToNext()) {
                out.add(
                    Row(
                        c.getString(0), c.getString(1), c.getString(2), c.getString(3),
                        if (c.isNull(4)) null else c.getLong(4),
                        if (c.isNull(5)) null else c.getString(5),
                        c.getLong(6), c.getInt(7)
                    )
                )
            }
        }
        return out
    }

    data class Row(
        val id: String, val pkg: String, val title: String?, val text: String?,
        val amount: Long?, val source: String?, val createdAt: Long, val tries: Int
    )
}
