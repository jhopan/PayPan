package id.jhopanstore.notiflisten

import android.app.Activity
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.graphics.Color
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ListView
import android.widget.TextView

/** Pilih app yang dipantau. Kosong = pantau semua. */
class AppPickerActivity : Activity() {

    private data class App(val label: String, val pkg: String)

    private val apps = ArrayList<App>()
    private val shown = ArrayList<App>()
    private lateinit var wl: MutableSet<String>
    private lateinit var adapter: BaseAdapter

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val prefs = getSharedPreferences("wl", 0)
        wl = HashSet(prefs.getStringSet("apps", emptySet()) ?: emptySet())

        val pm = packageManager
        val installed = pm.getInstalledApplications(0)
        for (ai in installed) {
            if (ai.packageName == packageName) continue
            val label = try { pm.getApplicationLabel(ai).toString() } catch (_: Exception) { ai.packageName }
            apps.add(App(label, ai.packageName))
        }
        apps.sortWith(compareBy({ it.label.lowercase() }))

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Color.WHITE)
        }
        val search = EditText(this).apply { hint = "Cari app..." }
        root.addView(search)

        adapter = object : BaseAdapter() {
            override fun getCount() = shown.size
            override fun getItem(p: Int) = shown[p]
            override fun getItemId(p: Int) = p.toLong()
            override fun getView(p: Int, convertView: View?, parent: ViewGroup?): View {
                val cb = convertView as? CheckBox
                    ?: CheckBox(this@AppPickerActivity)
                val a = shown[p]
                cb.text = "${a.label} (${a.pkg})"
                cb.isChecked = a.pkg in wl
                cb.setOnCheckedChangeListener { _, checked ->
                    if (checked) wl.add(a.pkg) else wl.remove(a.pkg)
                    prefs.edit().putStringSet("apps", HashSet(wl)).apply()
                }
                return cb
            }
        }
        val list = ListView(this)
        list.adapter = adapter
        root.addView(list, LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        search.addTextChangedListener(object : android.text.TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
            override fun onTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
            override fun afterTextChanged(s: android.text.Editable?) {
                shown.clear()
                val q = s?.toString()?.lowercase() ?: ""
                for (a in apps) {
                    if (q.isBlank() || a.label.lowercase().contains(q) || a.pkg.contains(q)) shown.add(a)
                }
                adapter.notifyDataSetChanged()
            }
        })
        shown.addAll(apps)

        setContentView(root)
    }
}
