/**
 * PayPan → Google Sheets Backup (Apps Script Web App)
 *
 * SETUP (sekali saja):
 * 1. Buat spreadsheet baru di Google Sheets, beri nama apa saja (mis. "PayPan Backup")
 * 2. Extensions → Apps Script
 * 3. Paste seluruh file ini, ganti SECRET di bawah dengan secret pilihan lo
 *    (harus sama dengan yang di admin PayPan → Konfigurasi → Backup Google Sheets)
 * 4. Deploy → New deployment → Web app:
 *      - Execute as: Me
 *      - Who has access: Anyone
 *    → Copy Web App URL (berakhiran /exec)
 * 5. Paste URL + secret tersebut ke admin PayPan → Konfigurasi → Backup Google Sheets
 *
 * PAYPAN mengirim: {secret, month: "2026-09", orders: [{id,tanggal,jam,harga,kode,total,status,bayar,source,expired_at}]}
 * Script ini: cari/bikin spreadsheet "PayPan Backup YYYY-MM",
 *             cari/bikin sheet "Minggu N (dd-dd)" sesuai tanggal order,
 *             upsert baris by id (idempotent — kirim ulang tidak menduplikasi).
 */

const SECRET = "GANTI-SECRET-INI";

function doPost(e) {
  try {
    const body = JSON.parse(e.postData.contents);
    if (body.secret !== SECRET) {
      return _json({ ok: false, error: "secret salah" });
    }
    if (!body.month || !Array.isArray(body.orders)) {
      return _json({ ok: false, error: "payload tidak lengkap" });
    }
    const ss = _getOrCreateSpreadsheet("PayPan Backup " + body.month);
    let inserted = 0, updated = 0, skipped = 0;

    // kelompokkan order per minggu (1-7, 8-14, 15-21, 22-28, 29-31)
    const byWeek = {};
    for (const o of body.orders) {
      const day = parseInt(o.tanggal.split(" ")[0], 10);
      if (isNaN(day)) continue;
      const week = Math.min(5, Math.floor((day - 1) / 7) + 1);
      (byWeek[week] = byWeek[week] || []).push(o);
    }

    for (const weekStr of Object.keys(byWeek)) {
      const week = parseInt(weekStr, 10);
      const first = new Date(body.month + "-01T00:00:00");
      const startDay = (week - 1) * 7 + 1;
      const endDate = new Date(first.getFullYear(), first.getMonth(), Math.min(startDay + 6, _daysInMonth(first)));
      const label = `Minggu ${week} (${String(startDay).padStart(2, "0")}-${String(endDate.getDate()).padStart(2, "0")})`;
      const sheet = _getOrCreateWeekSheet(ss, label);
      const res = _upsertRows(sheet, byWeek[week]);
      inserted += res.inserted;
      updated += res.updated;
      skipped += res.skipped;
    }

    _updateRingkasan(ss, body.month);
    return _json({ ok: true, inserted, updated, skipped });
  } catch (err) {
    return _json({ ok: false, error: String(err) });
  }
}

// GET untuk test koneksi dari browser (harus tampil "paypan backup siap")
function doGet() {
  return ContentService.createTextOutput("paypan backup siap");
}

function _json(obj) {
  return ContentService.createTextOutput(JSON.stringify(obj)).setMimeType(ContentService.MimeType.JSON);
}

function _getOrCreateSpreadsheet(name) {
  const it = DriveApp.getFolders(); // tak dipakai; cari by name di root Drive
  const files = DriveApp.searchFiles('name = "' + name + '" and mimeType = "application/vnd.google-apps.spreadsheet" and trashed = false');
  if (files.hasNext()) return SpreadsheetApp.open(files.next());
  return SpreadsheetApp.create(name);
}

function _getOrCreateWeekSheet(ss, label) {
  let sh = ss.getSheetByName(label);
  if (!sh) {
    sh = ss.insertSheet(label);
    sh.appendRow(["ID Invoice", "Tanggal", "Jam", "Harga", "Kode", "Total", "Status", "Dibayar", "Sumber", "Hangus"]);
    sh.getRange("A1:J1").setFontWeight("bold").setBackground("#eef2f6");
    sh.setFrozenRows(1);
    sh.setColumnWidth(1, 140); sh.setColumnWidth(10, 130);
  }
  return sh;
}

const HEADERS = ["id", "tanggal", "jam", "harga", "kode", "total", "status", "bayar", "source", "expired_at"];

function _upsertRows(sheet, orders) {
  const lastRow = Math.max(sheet.getLastRow(), 1);
  const idCol = sheet.getRange(2, 1, Math.max(lastRow - 1, 1), 1).getValues();
  const index = {};
  for (let i = 0; i < idCol.length; i++) {
    if (idCol[i][0]) index[idCol[i][0]] = i + 2; // row number
  }
  let inserted = 0, updated = 0, skipped = 0;
  const newRows = [];
  for (const o of orders) {
    const row = [o.id, o.tanggal, o.jam, o.harga, o.kode, o.total, o.status, o.bayar, o.source, o.expired_at];
    if (index[o.id]) {
      // update hanya jika status berubah (mis. pending -> paid)
      const cur = sheet.getRange(index[o.id], 1, 1, HEADERS.length).getValues()[0];
      if (cur[6] !== o.status || cur[7] !== o.bayar) {
        sheet.getRange(index[o.id], 1, 1, HEADERS.length).setValues([row]);
        updated++;
      } else {
        skipped++;
      }
    } else {
      newRows.push(row);
      index[o.id] = -1; // tandai agar tidak dobel dalam batch yang sama
      inserted++;
    }
  }
  if (newRows.length) {
    sheet.getRange(sheet.getLastRow() + 1, 1, newRows.length, HEADERS.length).setValues(newRows);
  }
  return { inserted, updated, skipped };
}

function _updateRingkasan(ss, month) {
  let sh = ss.getSheetByName("Ringkasan");
  if (!sh) {
    sh = ss.insertSheet("Ringkasan", 0); // taruh paling depan
    sh.appendRow(["Sheet", "Jumlah Invoice", "Lunas", "Expired", "Pendapatan"]);
    sh.getRange("A1:E1").setFontWeight("bold").setBackground("#eef2f6");
    sh.setFrozenRows(1);
  }
  // hitung dari tiap sheet minggu
  const sheets = ss.getSheets();
  let row = 2;
  for (const s of sheets) {
    if (s.getName() === "Ringkasan") continue;
    const last = s.getLastRow();
    if (last < 2) continue;
    const data = s.getRange(2, 1, last - 1, 10).getValues();
    let lunas = 0, expired = 0, pendapatan = 0;
    for (const r of data) {
      if (r[6] === "paid") { lunas++; pendapatan += r[5]; }
      else if (r[6] === "expired") expired++;
    }
    sh.getRange(row, 1, 1, 5).setValues([[s.getName(), data.length, lunas, expired, pendapatan]]);
    row++;
  }
}

function _daysInMonth(d) {
  return new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate();
}

// (opsional) jalankan manual dari editor untuk test tanpa PayPan:
function testManual() {
  const fake = {
    postData: { contents: JSON.stringify({
      secret: SECRET,
      month: Utilities.formatDate(new Date(), Session.getScriptTimeZone(), "yyyy-MM"),
      orders: [{ id: "TEST-001", tanggal: "15 Sep 2026", jam: "14:30", harga: 10000, kode: "021", total: 10021, status: "paid", bayar: "15 Sep 14:32", source: "GoPay Merchant", expired_at: "" }]
    }) }
  };
  Logger.log(doPost(fake).getContent());
}
