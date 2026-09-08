package id.jhopanstore.notiflisten

import android.app.Activity
import android.content.ComponentName
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.service.notification.NotificationListenerService
import android.util.Log
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast

class MainActivity : Activity() {

    private lateinit var db: Db
    private lateinit var tvStatus: TextView
    private lateinit var tvRecent: TextView
    private lateinit var etUrl: EditText
    private lateinit var etToken: EditText

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        db = Db(this)
        tvStatus = findViewById(R.id.tvStatus)
        tvRecent = findViewById(R.id.tvRecent)
        etUrl = findViewById(R.id.etUrl)
        etToken = findViewById(R.id.etToken)

        etUrl.setText(SenderService.url(this))
        etToken.setText(SenderService.token(this))

        findViewById<Button>(R.id.btnAccess).setOnClickListener {
            startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS))
        }
        findViewById<Button>(R.id.btnBattery).setOnClickListener {
            val pm = getSystemService(PowerManager::class.java)
            if (!pm.isIgnoringBatteryOptimizations(packageName)) {
                startActivity(
                    Intent(
                        Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                        Uri.parse("package:$packageName")
                    )
                )
            } else {
                Toast.makeText(this, "Optimasi baterai sudah off", Toast.LENGTH_SHORT).show()
            }
        }
        findViewById<Button>(R.id.btnApps).setOnClickListener {
            startActivity(Intent(this, AppPickerActivity::class.java))
        }
        findViewById<Button>(R.id.btnSave).setOnClickListener {
            SenderService.save(this, etUrl.text.toString().trim(), etToken.text.toString().trim())
            Toast.makeText(this, "Tersimpan", Toast.LENGTH_SHORT).show()
        }
        findViewById<Button>(R.id.btnTest).setOnClickListener {
            SenderService.save(this, etUrl.text.toString().trim(), etToken.text.toString().trim())
            val id = "test|${System.currentTimeMillis()}"
            db.insert(id, packageName, "TEST", "Test notif Rp18.017 dari app", 18017L, "TEST", System.currentTimeMillis())
            SenderService.kick(this)
            Toast.makeText(this, "Row test dibuat, sender dikick", Toast.LENGTH_SHORT).show()
            refresh()
        }
    }

    override fun onResume() {
        super.onResume()
        // setelah force-stop/reboot, system kadang tidak rebind otomatis.
        // app aktif minta rebind kalau listener mati padahal izin aktif.
        val enabled = android.provider.Settings.Secure.getString(
            contentResolver, "enabled_notification_listeners"
        )?.contains(packageName) == true
        if (enabled && !ListenerService.alive) {
            try {
                Log.w("NotifListen", "requestRebind from activity")
                NotificationListenerService.requestRebind(ComponentName(this, ListenerService::class.java))
            } catch (e: Exception) {
                Log.e("NotifListen", "rebind failed", e)
            }
        }
        refresh()
    }

    private fun refresh() {
        val enabled = android.provider.Settings.Secure.getString(
            contentResolver, "enabled_notification_listeners"
        )?.contains(packageName) == true
        val (p, s, f) = db.counts()
        val wl = getSharedPreferences("wl", 0).getStringSet("apps", emptySet()) ?: emptySet()
        tvStatus.text =
            "Izin listener: ${if (enabled) "AKTIF" else "BELUM"}\n" +
            "Listener: ${if (ListenerService.alive) "jalan" else "mati"}\n" +
            "App dipantau: ${if (wl.isEmpty()) "semua (belum pilih)" else wl.size}\n" +
            "Outbox: pending=$p sent=$s failed=$f"

        val sb = StringBuilder("Notif terakhir:\n")
        for (r in db.recent(5)) {
            sb.append("- [${r.pkg}] ${r.title}: ${(r.text ?: "").take(40)}\n")
        }
        sb.append("\nNotifListen-Payment v1.0 · JhopanStore")
        tvRecent.text = sb.toString()
    }
}
