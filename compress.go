package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"strings"
)

const (
	qrisMaxUploadBase64 = 8_000_000  // ~5.8 MB gambar mentah (base64 = +33%)
	qrisTargetStored    = 500_000    // target ukuran data-URL tersimpan
	qrisMaxDim          = 1200       // sisi terpanjang setelah downscale
)

// compressQRImage: terima data URL apa pun (PNG/JPG), hasilkan versi hemat.
// Urutan: downscale ke qrisMaxDim (nearest — QR tetap tajam), lalu JPEG
// dengan quality menurun sampai muat target. Gambar kecil dilewati apa adanya.
func compressQRImage(dataURL string) (string, bool) {
	if len(dataURL) <= qrisTargetStored {
		return dataURL, false
	}
	i := strings.Index(dataURL, "base64,")
	if i < 0 {
		return dataURL, false
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[i+7:])
	if err != nil {
		return dataURL, false
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return dataURL, false
	}
	img = scaleDown(img, qrisMaxDim)
	for q := 90; q >= 40; q -= 10 {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}) != nil {
			continue
		}
		out := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		if len(out) <= qrisTargetStored {
			return out, true
		}
	}
	// q40 pun masih besar: pakai q40 terakhir (lebih baik tersimpan besar dari pada gagal)
	var buf bytes.Buffer
	if jpeg.Encode(&buf, img, &jpeg.Options{Quality: 40}) != nil {
		return dataURL, false
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// scaleDown: nearest-neighbor ke maxSide (hanya mengecil).
func scaleDown(img image.Image, maxSide int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return img
	}
	nw, nh := w, h
	if w >= h {
		nw = maxSide
		nh = h * maxSide / w
	} else {
		nh = maxSide
		nw = w * maxSide / h
	}
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			out.Set(x, y, img.At(sx, sy))
		}
	}
	return out
}
