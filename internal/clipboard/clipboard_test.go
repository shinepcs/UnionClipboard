package clipboard

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"testing"
	"time"
)

func TestDIBRoundTripAndThumbnail(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			src.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 4),
				G: uint8(y * 8),
				B: 120,
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	origPNG := buf.Bytes()

	dib, err := PNGToDIB(origPNG)
	if err != nil {
		t.Fatalf("PNGToDIB: %v", err)
	}
	roundPNG, err := DIBToPNG(dib)
	if err != nil {
		t.Fatalf("DIBToPNG: %v", err)
	}

	decoded, err := png.Decode(bytes.NewReader(roundPNG))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 64 || decoded.Bounds().Dy() != 32 {
		t.Fatalf("unexpected bounds: %+v", decoded.Bounds())
	}
	c := color.NRGBAModel.Convert(decoded.At(10, 5)).(color.NRGBA)
	if c.R != 40 || c.G != 40 || c.B != 120 || c.A != 255 {
		t.Fatalf("pixel mismatch at (10,5): %+v", c)
	}

	thumb, err := MakeThumbnailPNG(roundPNG, 32, 32)
	if err != nil {
		t.Fatalf("MakeThumbnailPNG: %v", err)
	}
	thImg, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if thImg.Bounds().Dx() != 32 || thImg.Bounds().Dy() != 16 {
		t.Fatalf("expected 32x16 thumbnail, got %+v", thImg.Bounds())
	}
}

func TestHDROPRoundTrip(t *testing.T) {
	paths := []string{
		`C:\Users\홍길동\Documents\테스트 파일.txt`,
		`D:\Shared Folder\image (1).png`,
	}
	raw, err := EncodeHDROP(paths)
	if err != nil {
		t.Fatalf("EncodeHDROP: %v", err)
	}
	got, err := DecodeHDROP(raw)
	if err != nil {
		t.Fatalf("DecodeHDROP: %v", err)
	}
	if !reflect.DeepEqual(got, paths) {
		t.Fatalf("expected %v, got %v", paths, got)
	}
}

func TestLoopSuppressor(t *testing.T) {
	ls := NewLoopSuppressor(60 * time.Millisecond)
	ls.Suppress("abc")
	if !ls.IsSuppressed("abc") {
		t.Fatal("expected abc to be suppressed immediately")
	}
	if ls.IsSuppressed("xyz") {
		t.Fatal("did not expect xyz to be suppressed")
	}
	time.Sleep(80 * time.Millisecond)
	if ls.IsSuppressed("abc") {
		t.Fatal("expected abc suppression to expire")
	}
}
