package id.jhopanstore.notiflisten

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.IBinder
import android.service.notification.NotificationListenerService
import android.util.Log
import org.json.JSONObject
import java.io.OutputStreamWriter
import java.net.HttpURLConnection
import java.net.URL

/**
 * Foreground service "relay aktif". Loop per 60 detik baca outbox pending,
 * POST ke server, update status. kick() memicu loop segera tanpa menunggu tick.
 * Di-start saat boot + saat listener menerima notif pertama.
 */
class SenderService : Service() {

    companion object {
        const val TAG = "NotifListen"
        const val CHANNEL = "relay"
        private const val PREFS = "cfg"
        private const val KEY_URL = "url"
        private const val KEY_TOKEN = "token"

        @Volatile private var running = false
        @Volatile private var worker: Thread? = null
        @Volatile private var wake: Object? = null

        /** bangunkan worker segera: interrupt sleep 60s */
        fun kick(ctx: Context) {
            wake?.let { w ->
                synchronized(w) { w.notifyAll() }
            }
            try {
                val i = Intent(ctx, SenderService::class.java)
                if (android.os.Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i)
                else ctx.startService(i)
            } catch (_: Exception) {
            }
        }

        fun url(ctx: Context): String = ctx.getSharedPreferences(PREFS, 0).getString(KEY_URL, "") ?: ""
        fun token(ctx: Context): String = ctx.getSharedPreferences(PREFS, 0).getString(KEY_TOKEN, "") ?: ""
        fun save(ctx: Context, url: String, token: String) {
            ctx.getSharedPreferences(PREFS, 0).edit().putString(KEY_URL, url).putString(KEY_TOKEN, token).apply()
        }
    }

    private lateinit var db: Db

    override fun onCreate() {
        super.onCreate()
        db = Db(this)
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startAsForeground()
        if (!running) {
            running = true
            val w = Object()
            wake = w
            worker = Thread {
                try {
                    loop()
                } finally {
                    running = false
                }
            }
            worker?.start()
        }
        // START_STICKY: service dibunuh system -> direstart otomatis dengan intent null
        return START_STICKY
    }

    private fun startAsForeground() {
        val nm = getSystemService(NotificationManager::class.java)
        if (android.os.Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, "Relay pembayaran", NotificationManager.IMPORTANCE_LOW)
            )
        }
        val b = Notification.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentTitle("NotifListen Payment")
            .setContentText("Relay notifikasi aktif")
            .setOngoing(true)
        when {
            android.os.Build.VERSION.SDK_INT >= 34 ->
                startForeground(1, b.build(), android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
            android.os.Build.VERSION.SDK_INT >= 29 ->
                startForeground(1, b.build(), 0)
            else ->
                startForeground(1, b.build())
        }
    }

    private fun loop() {
        var watchdogTick = 0
        while (true) {
            try {
                val u = url(this)
                val tk = token(this)
                if (u.isBlank()) {
                    // belum dikonfigurasi: tidur panjang, jangan buang baterai
                    sleepOrWake(300_000)
                    continue
                }
                // watchdog: listener unbind diam-diam (quirk OEM) -> minta rebind tiap ~5 menit
                if (++watchdogTick % 5 == 0 && !ListenerService.alive) {
                    try {
                        NotificationListenerService.requestRebind(
                            ComponentName(this, ListenerService::class.java)
                        )
                    } catch (_: Exception) {}
                }
                db.cleanup()
                val rows = db.pending(20)
                var anyFail = false
                for (r in rows) {
                    if (sendOne(u, tk, r)) db.markSent(r.id) else anyFail = true
                }
                sleepOrWake(if (anyFail) 15_000 else 60_000)
            } catch (e: Exception) {
                Log.e(TAG, "loop", e)
                sleepOrWake(30_000)
            }
        }
    }

    private fun sleepOrWake(ms: Long) {
        val w = wake ?: return
        synchronized(w) {
            try {
                w.wait(ms)
            } catch (_: InterruptedException) {
                // dibangunkan kick(): langsung lanjut loop
            }
        }
    }

    private fun sendOne(u: String, tk: String, r: Db.Row): Boolean {
        val j = JSONObject()
        j.put("id", r.id)
        j.put("pkg", r.pkg)
        j.put("title", r.title ?: JSONObject.NULL)
        j.put("text", r.text ?: JSONObject.NULL)
        if (r.amount != null) j.put("amount", r.amount) else j.put("amount", JSONObject.NULL)
        if (r.source != null) j.put("source", r.source) else j.put("source", JSONObject.NULL)
        j.put("created_at", r.createdAt)

        var code = -1
        try {
            val conn = URL(u).openConnection() as HttpURLConnection
            conn.requestMethod = "POST"
            conn.connectTimeout = 15_000
            conn.readTimeout = 15_000
            conn.doOutput = true
            conn.setRequestProperty("Content-Type", "application/json")
            if (tk.isNotBlank()) conn.setRequestProperty("Authorization", "Bearer $tk")
            OutputStreamWriter(conn.outputStream, Charsets.UTF_8).use { it.write(j.toString()) }
            code = conn.responseCode
            conn.inputStream.use { it.readBytes() } // drain
            conn.disconnect()
        } catch (e: Exception) {
            // kegagalan JARINGAN (offline/DNS/timeout): JANGAN hitung tries.
            // row tetap pending selamanya sampai inet balik (batas: umur 48 jam di query pending)
            return false
        }
        if (code in 200..299) return true
        // server MENOLAK (4xx/5xx): hitung tries, cap 8 -> failed
        db.markFailed(r.id, "HTTP $code")
        return false
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        // swipe dari Recents: restart diri biar relay tetap hidup
        kick(applicationContext)
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        running = false
        super.onDestroy()
    }
}
