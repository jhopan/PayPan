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
        // burst: 12 notif beda format + amount, ditembakkan cepat berturut-turut
        val burst = listOf(
            Pair("BCA", "Transfer masuk dari 0812345678. Nominal: Rp 18.537. Saldo: Rp 1.250.000"),
            Pair("DANA", "Pembayaran DANA berhasil! Rp 75.500 ke Toko Jhopan"),
            Pair("BCA", "Anda menerima transfer CR Rp 1.000.000 dari BUDI. Saldo: Rp 2.500.000"),
            Pair("OVO", "Rp 250.000 masuk dari OVO"),
            Pair("ShopeePay", "Kamu menerima Rp 89.999 dari ShopeePay"),
            Pair("BRI", "Saldo Rp 500.000. Transfer masuk Rp 12.345 dari BRI"),
            Pair("BRIVA", "BRIVA 8800812345678901 Rp 45.678 ditransfer"),
            Pair("BCA", "Anda menerima transfer Rp18.017 dari 0812xxxx. Saldo Rp250.000"),
            Pair("GoPay Merchant", "Pembayaran QRIS statis diterima Rp 1.001 di J Store."),
            Pair("GoPay", "Top up GoPay Rp 100.000 berhasil"),
            Pair("Telkomsel", "Paket data 5GB aktif. Kuota 5GB/30 hari"),
            Pair("Mandiri", "Transfer masuk Rp 5.000,50 dari Mandiri Livin'")
        )
        for ((i, p) in burst.withIndex()) {
            post(nm, (System.currentTimeMillis() % 100000).toInt() + i, p.first, p.second)
            Thread.sleep(80)
        }
        finish()
    }

    private fun post(nm: NotificationManager, id: Int, title: String, text: String) {
        val b = if (android.os.Build.VERSION.SDK_INT >= 26)
            Notification.Builder(this, "t") else Notification.Builder(this)
        b.setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(title)
            .setContentText(text.take(40))
            .setStyle(Notification.BigTextStyle().bigText(text))
        nm.notify(id, b.build())
    }
}
