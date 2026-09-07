package id.jhopanstore.testnotify

import android.app.Activity
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Bundle

class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val nm = getSystemService(NotificationManager::class.java)
        if (android.os.Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(NotificationChannel("t", "test", NotificationManager.IMPORTANCE_DEFAULT))
        }
        post(nm, "BCA", "Transfer masuk dari 0812345678. Nominal: Rp 18.537. Saldo: Rp 1.250.000")
        post(nm, "DANA", "Pembayaran DANA berhasil! Rp 75.500 ke Toko Jhopan")
        finish()
    }

    private fun post(nm: NotificationManager, title: String, text: String) {
        val b = if (android.os.Build.VERSION.SDK_INT >= 26)
            Notification.Builder(this, "t") else Notification.Builder(this)
        b.setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(Notification.BigTextStyle().bigText(text))
        nm.notify(title.hashCode(), b.build())
    }
}
