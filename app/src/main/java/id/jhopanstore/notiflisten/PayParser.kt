package id.jhopanstore.notiflisten

import java.util.regex.Pattern

/**
 * Parser notif pembayaran Indonesia.
 * ponytail: map per-app ditambah seiring contoh notif nyata masuk.
 * Tambahkan entry baru di PARSER + APP_NAMES saat dapat sampel.
 */
object PayParser {

    private val AMOUNT: Pattern = Pattern.compile("(?:Rp|IDR)\\.?\\s*([\\d.,]+)")

    // notif GoPay Merchant yang BUKAN pembayaran masuk — jangan dikirim.
    // (pencairan dana, top up saldo, refund keluar, dsb.)
    private val IGNORE_PATTERNS = listOf(
        "pencairan dana",
        "pencairan berhasil",
        "top up saldo",
        "topup berhasil",
        "deposit berhasil",
        "penarikan dana",
        "tarik dana",
        "refund"
    )

    /** true kalau notif ini bukan pembayaran masuk (skip). */
    fun isNonPayment(title: String?, text: String): Boolean {
        val hay = ((title ?: "") + " " + text).lowercase()
        return IGNORE_PATTERNS.any { hay.contains(it) }
    }

    // (regex title|text case-insensitive, source label) — rule spesifik SEBELUM rule umum
    private val SOURCE_RULES = listOf(
        Pair("gopaymerchant", "GoPay Merchant"),
        Pair("dana", "DANA"),
        Pair("gopay|gojek", "GoPay"),
        Pair("ovo", "OVO"),
        Pair("shopeepay|shopee", "ShopeePay"),
        Pair("bca", "BCA"),
        Pair("bri|membri|brimo", "BRI"),
        Pair("bni", "BNI"),
        Pair("mandiri|livin", "Mandiri"),
        Pair("seabank|sea", "SeaBank"),
        Pair("jenius", "Jenius"),
        Pair("linkaja", "LinkAja")
    )

    fun parseAmount(text: String?): Long? {
        if (text == null) return null
        val m = AMOUNT.matcher(text)
        var first: Long? = null
        var withKeyword: Long? = null
        while (m.find()) {
            // buang titik/koma nggantung: "Rp 18.537. Saldo" -> "18.537"
            var raw = m.group(1).trimEnd('.', ',')
            if (raw.isEmpty() || raw.all { it == '.' || it == ',' }) continue
            if (first == null) first = normalize(raw)
            if (withKeyword == null) {
                val before = text.substring(0, m.start()).takeLast(30).lowercase()
                if (listOf("nominal", "transfer", "masuk", "bayar", "menerima", "total", "amount").any { before.contains(it) }) {
                    withKeyword = normalize(raw)
                }
            }
        }
        return withKeyword ?: first
    }

    /** 1.500.000 -> 1500000 ; 1.500,50 -> 1500.50 ; 15.000.50 -> 15000.50 */
    fun normalize(raw: String): Long? {
        var s = raw.trim().trimStart('.')
        if (s.isEmpty()) return null
        val hasComma = s.contains(',')
        val hasDot = s.contains('.')
        var value: Long
        if (hasComma) {
            val parts = s.split(',')
            val intPart = parts[0].replace(".", "")
            val frac = if (parts.size > 1) parts[1].take(2) else ""
            value = (intPart.toLongOrNull() ?: return null) * 100 + (frac.padEnd(2, '0').toLongOrNull() ?: 0L)
            value /= 100
        } else if (hasDot) {
            val lastDot = s.lastIndexOf('.')
            val tail = s.substring(lastDot + 1)
            value = if (tail.length == 3 && s.removeSuffix(tail).count { it == '.' } >= 1 || (tail.length == 3 && s.length > 4)) {
                // titik ribuan: 1.500.000
                s.replace(".", "").toLongOrNull() ?: return null
            } else {
                // titik desimal: 15000.50
                val p = s.split(".")
                (p[0].toLongOrNull() ?: return null) * 100 / 100 // integer only
            }
        } else {
            value = s.toLongOrNull() ?: return null
        }
        return value
    }

    fun parseSource(pkg: String, title: String?, text: String?): String? {
        val hay = ((title ?: "") + " " + (text ?: "")).lowercase()
        for ((pat, label) in SOURCE_RULES) {
            // pola bisa multi-alternasi "bri|membri|brimo" — cek per alternatif
            if (pat.split("|").any { hay.contains(it) }) return label
        }
        return null
    }
}
