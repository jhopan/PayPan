package id.jhopanstore.notiflisten

import android.app.Notification
import android.content.ComponentName
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.util.Log

/**
 * Jalur panas. Aturan: SECEPAT MUNGKIN keluar dari callback ini.
 * Insert SQLite lalu selesai. Parse amount/source dilakukan sebelum insert
 * (regex murah), pengiriman lewat SenderService, bukan di sini.
 */
class ListenerService : NotificationListenerService() {

    companion object {
        const val TAG = "NotifListen"
        var alive = false
            private set
    }

    private lateinit var db: Db

    override fun onCreate() {
        super.onCreate()
        db = Db(this)
        alive = true
    }

    override fun onDestroy() {
        alive = false
        super.onDestroy()
    }

    override fun onListenerConnected() {
        Log.i(TAG, "listener connected")
    }

    override fun onListenerDisconnected() {
        // API 24+: request rebind segera agar listener tidak lepas diam-diam
        Log.w(TAG, "listener disconnected, requesting rebind")
        requestRebind(ComponentName(this, ListenerService::class.java))
    }

    override fun onNotificationPosted(sbn: StatusBarNotification) {
        try {
            val pkg = sbn.packageName ?: return
            if (pkg == packageName) return

            // whitelist: kosong = semua; terisi = hanya app terpilih
            val wl = getSharedPreferences("wl", 0).getStringSet("apps", emptySet()) ?: emptySet()
            if (wl.isNotEmpty() && pkg !in wl) return

            val extras = sbn.notification.extras

            // buang ongoing + summary grup (bukan notif transaksi)
            if (sbn.isOngoing) return
            if (sbn.notification.flags and Notification.FLAG_GROUP_SUMMARY != 0) return

            val title = extras.getCharSequence(Notification.EXTRA_TITLE)?.toString()
            val big = extras.getCharSequence(Notification.EXTRA_BIG_TEXT)?.toString()
            val text = extras.getCharSequence(Notification.EXTRA_TEXT)?.toString()
            val body = when {
                !big.isNullOrBlank() -> big
                !text.isNullOrBlank() -> text
                else -> return
            }

            // notif non-pembayaran (pencairan dana, top up, refund) — skip
            if (PayParser.isNonPayment(title, body)) return

            val amount = PayParser.parseAmount(body)
            val source = PayParser.parseSource(pkg, title, body)
            // id = key notif (stabil saat notif di-update) + hash konten.
            // konten sama -> id sama -> INSERT OR IGNORE dedup, postTime tidak dipakai
            val hash = Integer.toHexString(
                (title.orEmpty() + "|" + body).hashCode()
            )
            val id = (sbn.key ?: "$pkg|${sbn.id}") + "|" + hash

            db.insert(id, pkg, title, body, amount, source, sbn.postTime)
            SenderService.kick(this)
        } catch (e: Exception) {
            Log.e(TAG, "onNotificationPosted", e)
        }
    }
}
