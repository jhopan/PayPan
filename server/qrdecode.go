package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// decodeQRFromDataURL: baca payload QR dari gambar upload (data URL base64 PNG/JPG).
func decodeQRFromDataURL(dataURL string) (string, error) {
	i := strings.Index(dataURL, "base64,")
	if i < 0 {
		return "", errBadImage
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[i+7:])
	if err != nil {
		return "", err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	res, err := qrcode.NewQRCodeReader().DecodeWithoutHints(bmp)
	if err != nil {
		return "", err
	}
	return res.GetText(), nil
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const errBadImage = simpleErr("bukan data URL gambar")
