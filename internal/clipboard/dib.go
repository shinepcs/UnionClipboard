package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/bits"
)

const (
	biRGB       = 0
	biBitfields = 3
)

// PNGToDIB converts PNG bytes into a 32-bit bottom-up Windows BITMAPINFOHEADER CF_DIB buffer.
func PNGToDIB(pngBytes []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty image")
	}

	const headerSize = 40
	pixelBytes := w * h * 4
	out := make([]byte, headerSize+pixelBytes)

	binary.LittleEndian.PutUint32(out[0:4], headerSize)
	binary.LittleEndian.PutUint32(out[4:8], uint32(w))
	binary.LittleEndian.PutUint32(out[8:12], uint32(h)) // positive = bottom-up
	binary.LittleEndian.PutUint16(out[12:14], 1)        // planes
	binary.LittleEndian.PutUint16(out[14:16], 32)       // bpp
	binary.LittleEndian.PutUint32(out[16:20], biRGB)
	binary.LittleEndian.PutUint32(out[20:24], uint32(pixelBytes))

	for y := 0; y < h; y++ {
		srcY := b.Min.Y + (h - 1 - y)
		rowOff := headerSize + y*w*4
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, srcY)).(color.NRGBA)
			off := rowOff + x*4
			out[off+0] = c.B
			out[off+1] = c.G
			out[off+2] = c.R
			out[off+3] = c.A
		}
	}
	return out, nil
}

// DIBToPNG converts Windows CF_DIB / CF_DIBV5 (24/32-bit BI_RGB or BI_BITFIELDS) bytes into PNG.
func DIBToPNG(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("dib too short")
	}
	hdrSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	if hdrSize < 40 || len(dib) < hdrSize {
		return nil, fmt.Errorf("invalid dib header size: %d", hdrSize)
	}
	w := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	rawH := int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	topDown := false
	h := rawH
	if rawH < 0 {
		topDown = true
		h = -rawH
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid dib dimensions %dx%d", w, h)
	}

	bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])
	if bpp != 24 && bpp != 32 {
		return nil, fmt.Errorf("unsupported dib bpp: %d", bpp)
	}
	if compression != biRGB && compression != biBitfields {
		return nil, fmt.Errorf("unsupported dib compression: %d", compression)
	}

	// Default masks for 32-bit BGRA
	rMask := uint32(0x00FF0000)
	gMask := uint32(0x0000FF00)
	bMask := uint32(0x000000FF)
	aMask := uint32(0xFF000000)

	pixelOffset := hdrSize
	if compression == biBitfields {
		if hdrSize == 40 {
			if len(dib) < 40+12 {
				return nil, errors.New("dib bitfields truncated")
			}
			rMask = binary.LittleEndian.Uint32(dib[40:44])
			gMask = binary.LittleEndian.Uint32(dib[44:48])
			bMask = binary.LittleEndian.Uint32(dib[48:52])
			aMask = 0
			pixelOffset += 12
		} else if hdrSize >= 56 {
			rMask = binary.LittleEndian.Uint32(dib[40:44])
			gMask = binary.LittleEndian.Uint32(dib[44:48])
			bMask = binary.LittleEndian.Uint32(dib[48:52])
			aMask = binary.LittleEndian.Uint32(dib[52:56])
		}
	}

	stride := ((w*bpp + 31) / 32) * 4
	if len(dib) < pixelOffset+stride*h {
		return nil, errors.New("dib pixel data truncated")
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	hasNonZeroAlpha := false

	for row := 0; row < h; row++ {
		dstY := h - 1 - row
		if topDown {
			dstY = row
		}
		rowOff := pixelOffset + row*stride
		for x := 0; x < w; x++ {
			if bpp == 24 {
				off := rowOff + x*3
				img.SetNRGBA(x, dstY, color.NRGBA{
					R: dib[off+2],
					G: dib[off+1],
					B: dib[off+0],
					A: 255,
				})
				hasNonZeroAlpha = true
			} else {
				px := binary.LittleEndian.Uint32(dib[rowOff+x*4 : rowOff+x*4+4])
				r := extractMaskedByte(px, rMask)
				g := extractMaskedByte(px, gMask)
				b := extractMaskedByte(px, bMask)
				a := byte(255)
				if aMask != 0 {
					a = extractMaskedByte(px, aMask)
					if a != 0 {
						hasNonZeroAlpha = true
					}
				} else {
					hasNonZeroAlpha = true
				}
				img.SetNRGBA(x, dstY, color.NRGBA{R: r, G: g, B: b, A: a})
			}
		}
	}

	// Many Win32 apps write 32-bit BI_RGB DIBs with alpha byte set to 0 for opaque pixels.
	if bpp == 32 && !hasNonZeroAlpha {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				off := img.PixOffset(x, y)
				img.Pix[off+3] = 255
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func extractMaskedByte(px, mask uint32) byte {
	if mask == 0 {
		return 0
	}
	shift := bits.TrailingZeros32(mask)
	val := (px & mask) >> shift
	width := bits.OnesCount32(mask)
	if width == 8 {
		return byte(val)
	}
	if width == 0 {
		return 0
	}
	maxVal := (uint32(1) << width) - 1
	return byte((val * 255) / maxVal)
}
