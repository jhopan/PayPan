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
 * Script ini: cari/bikin spreadsheet "PayPan Backup <TAHUN>" (1 per tahun),
 *             tab per bulan ("September 2026"), di dalamnya blok per minggu.
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
    // 1 spreadsheet per TAHUN: "PayPan Backup 2026" (month = "2026-09" → tahun 2026)
    const year = body.month.split("-")[0];
    const ss = _getOrCreateSpreadsheet("PayPan Backup " + year);
    // tab per bulan: "September 2026"
    const monthNames = ["Januari","Februari","Maret","April","Mei","Juni","Juli","Agustus","September","Oktober","November","Desember"];
    const monthNum = parseInt(body.month.split("-")[1], 10);
    const monthSheetName = monthNames[monthNum - 1] + " " + year;
    const monthSheet = _getOrCreateMonthSheet(ss, monthSheetName);
    let inserted = 0, updated = 0, skipped = 0;

    // di dalam tab bulan: kelompokkan per minggu (1-7, 8-14, 15-21, 22-28, 29-31)
    // → blok dengan header "Minggu N (dd-dd)" lalu baris transaksi di bawahnya
    const byWeek = {};
    for (const o of body.orders) {
      const day = parseInt(o.tanggal.split(" ")[0], 10);
      if (isNaN(day)) continue;
      const week = Math.min(5, Math.floor((day - 1) / 7) + 1);
      (byWeek[week] = byWeek[week] || []).push(o);
    }

    for (const weekStr of Object.keys(byWeek).sort((a, b) => a - b)) {
      const week = parseInt(weekStr, 10);
      const first = new Date(body.month + "-01T00:00:00");
      const startDay = (week - 1) * 7 + 1;
      const endDate = new Date(first.getFullYear(), first.getMonth(), Math.min(startDay + 6, _daysInMonth(first)));
      const label = `Minggu ${week} (${String(startDay).padStart(2, "0")}-${String(endDate.getDate()).padStart(2, "0")})`;
      const res = _upsertWeekBlock(monthSheet, label, byWeek[week]);
      inserted += res.inserted;
      updated += res.updated;
      skipped += res.skipped;
    }

    _updateRingkasan(ss, monthSheetName);
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
  // 1 spreadsheet per TAHUN (mis. "PayPan Backup 2026").
  // Search by name di root Drive; kalau belum ada, buat baru.
  const files = DriveApp.searchFiles('name = "' + name + '" and mimeType = "application/vnd.google-apps.spreadsheet" and trashed = false');
  if (files.hasNext()) return SpreadsheetApp.open(files.next());
  const ss = SpreadsheetApp.create(name);
  // rapikan: hapus sheet default "Sheet1" kosong
  const def = ss.getSheetByName("Sheet1");
  if (def && ss.getSheets().length > 1) ss.deleteSheet(def);
  return ss;
}

// _getOrCreateMonthSheet: tab bulan di dalam spreadsheet tahunan ("September 2026").
// Struktur dalam tab: blok per minggu — header blok "Minggu N (dd-dd)" berwarna,
// di bawahnya header kolom, lalu baris transaksi. Upsert by id per blok.
function _getOrCreateMonthSheet(ss, name) {
  let sh = ss.getSheetByName(name);
  if (!sh) {
    sh = ss.insertSheet(name);
    sh.getRange("A1").setValue(name).setFontWeight("bold").setFontSize(14);
    sh.setFrozenRows(1);
  }
  return sh;
}

const HEADERS = ["ID Invoice", "Tanggal", "Jam", "Harga", "Kode", "Total", "Status", "Dibayar", "Sumber", "Hangus"];
const COL_HEADERS = ["ID Invoice", "Tanggal", "Jam", "Harga", "Kode", "Total", "Status", "Dibayar", "Sumber", "Hangus"];

// _findWeekBlock: cari baris header blok minggu ("Minggu N (dd-dd)") di tab bulan.
// Return row number header-nya, atau 0 kalau belum ada.
function _findWeekBlock(sheet, label) {
  const last = sheet.getLastRow();
  if (last < 1) return 0;
  const colA = sheet.getRange(1, 1, last, 1).getValues();
  for (let i = 0; i < colA.length; i++) {
    if (colA[i][0] === label) return i + 1;
  }
  return 0;
}

// _upsertWeekBlock: pastikan blok minggu ada (header + kolom + rows), upsert by id.
function _upsertWeekBlock(sheet, label, orders) {
  let headerRow = _findWeekBlock(sheet, label);
  if (headerRow === 0) {
    // append di akhir sheet + 1 baris kosong pemisah (kalau bukan sheet kosong)
    const startRow = sheet.getLastRow() + (sheet.getLastRow() > 0 ? 2 : 1);
    sheet.getRange(startRow, 1, 1, 1).setValue(label).setFontWeight("bold").setBackground("#eef2f6");
    sheet.getRange(startRow + 1, 1, 1, COL_HEADERS.length).setValues([COL_HEADERS])
      .setFontWeight("bold").setBackground("#f5f7fa");
    headerRow = startRow;
  }
  // data mulai headerRow+2 (baris 1 = label, baris 2 = kolom)
  const dataStart = headerRow + 2;
  const dataEnd = _findNextBlockStart(sheet, headerRow + 1);
  const existingRows = dataEnd > dataStart ? dataEnd - dataStart : 0;
  const index = {};
  if (existingRows > 0) {
    const idCol = sheet.getRange(dataStart, 1, existingRows, 1).getValues();
    for (let i = 0; i < idCol.length; i++) {
      if (idCol[i][0]) index[idCol[i][0]] = dataStart + i;
    }
  }
  let inserted = 0, updated = 0, skipped = 0;
  const newRows = [];
  for (const o of orders) {
    const row = [o.id, o.tanggal, o.jam, o.harga, o.kode, o.total, o.status, o.bayar, o.source, o.expired_at];
    if (index[o.id]) {
      const cur = sheet.getRange(index[o.id], 1, 1, COL_HEADERS.length).getValues()[0];
      if (cur[6] !== o.status || cur[7] !== o.bayar) {
        sheet.getRange(index[o.id], 1, 1, COL_HEADERS.length).setValues([row]);
        updated++;
      } else {
        skipped++;
      }
    } else {
      newRows.push(row);
      index[o.id] = -1; // cegah dobel dalam batch
      inserted++;
    }
  }
  if (newRows.length) {
    // sisipkan sekaligus sebelum blok minggu berikutnya — tidak merusak blok lain
    sheet.insertRowsBefore(dataStart + existingRows, newRows.length);
    sheet.getRange(dataStart + existingRows, 1, newRows.length, COL_HEADERS.length).setValues(newRows);
  }
  return { inserted, updated, skipped };
}

// _findNextBlockStart: dari fromRow, cari baris pertama yang berisi label blok minggu lain
// (pola "Minggu "). Return row tsb, atau row setelah data terakhir.
function _findNextBlockStart(sheet, fromRow) {
  const last = sheet.getLastRow();
  if (fromRow > last) return fromRow;
  const colA = sheet.getRange(fromRow, 1, last - fromRow + 1, 1).getValues();
  for (let i = 0; i < colA.length; i++) {
    const v = String(colA[i][0]);
    if (v.startsWith("Minggu ")) return fromRow + i;
  }
  return last + 1;
}

function _updateRingkasan(ss, monthName) {
  let sh = ss.getSheetByName("Ringkasan");
  if (!sh) {
    sh = ss.insertSheet("Ringkasan", 0); // taruh paling depan
    sh.appendRow(["Bulan", "Jumlah Invoice", "Lunas", "Expired", "Pendapatan"]);
    sh.getRange("A1:E1").setFontWeight("bold").setBackground("#eef2f6");
    sh.setFrozenRows(1);
  }
  // hitung dari tiap tab bulan (sheet yang BUKAN Ringkasan)
  const sheets = ss.getSheets();
  let row = 2;
  for (const s of sheets) {
    if (s.getName() === "Ringkasan") continue;
    const last = s.getLastRow();
    if (last < 1) continue;
    const data = s.getRange(1, 1, last, 10).getValues();
    let lunas = 0, expired = 0, pendapatan = 0, count = 0;
    for (const r of data) {
      // baris transaksi: kolom B = tanggal (bukan label blok / header kolom)
      if (typeof r[6] === "string" && (r[6] === "paid" || r[6] === "expired")) {
        count++;
        if (r[6] === "paid") { lunas++; pendapatan += r[5]; }
        else if (r[6] === "expired") expired++;
      }
    }
    sh.getRange(row, 1, 1, 5).setValues([[s.getName(), count, lunas, expired, pendapatan]]);
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
