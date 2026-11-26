package clipboard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
)

// NormalizeToPNG decodes PNG/JPEG/GIF bytes and re-encodes as canonical PNG with dimensions.
func NormalizeToPNG(raw []byte) (pngBytes []byte, width, height int, err error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}
	b := img.Bounds()
	width = b.Dx()
	height = b.Dy()
	if width <= 0 || height <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid image dimensions %dx%d", width, height)
	}
	if format == "png" {
		// Re-encode only if not already valid canonical PNG or keep raw PNG bytes
		return raw, width, height, nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, 0, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), width, height, nil
}

// MakeThumbnailPNG generates a smooth aspect-preserving thumbnail PNG fitting within maxW x maxH.
func MakeThumbnailPNG(pngBytes []byte, maxW, maxH int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, err
	}
	srcBounds := img.Bounds()
	sw := srcBounds.Dx()
	sh := srcBounds.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, fmt.Errorf("empty image")
	}
	if maxW <= 0 {
		maxW = 32
	}
	if maxH <= 0 {
		maxH = 32
	}

	dw, dh := sw, sh
	if sw > maxW || sh > maxH {
		scaleW := float64(maxW) / float64(sw)
		scaleH := float64(maxH) / float64(sh)
		scale := scaleW
		if scaleH < scale {
			scale = scaleH
		}
		dw = int(float64(sw)*scale + 0.5)
		dh = int(float64(sh)*scale + 0.5)
		if dw < 1 {
			dw = 1
		}
		if dh < 1 {
			dh = 1
		}
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := srcBounds.Min.Y + (y*sh)/dh
		for x := 0; x < dw; x++ {
			sx := srcBounds.Min.X + (x*sw)/dw
			c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
			dst.SetNRGBA(x, y, c)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
