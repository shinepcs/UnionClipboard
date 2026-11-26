package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"unicode/utf16"
)

// EncodeHDROP encodes file paths into a Windows CF_HDROP buffer (DROPFILES + UTF-16LE double-NUL).
func EncodeHDROP(paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return nil, errors.New("no paths to encode")
	}
	var buf bytes.Buffer
	// DROPFILES struct (20 bytes):
	// pFiles (DWORD=20), pt.x (LONG=0), pt.y (LONG=0), fNC (BOOL=0), fWide (BOOL=1)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(20))
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(1))

	for _, p := range paths {
		u := utf16.Encode([]rune(p))
		for _, ch := range u {
			_ = binary.Write(&buf, binary.LittleEndian, ch)
		}
		_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	}
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // double-NUL terminator
	return buf.Bytes(), nil
}

// DecodeHDROP parses a Windows CF_HDROP buffer back into file paths.
func DecodeHDROP(data []byte) ([]string, error) {
	if len(data) < 20 {
		return nil, errors.New("hdrop data too short")
	}
	pFiles := int(binary.LittleEndian.Uint32(data[0:4]))
	fWide := binary.LittleEndian.Uint32(data[16:20])
	if pFiles < 20 || pFiles >= len(data) {
		return nil, errors.New("invalid pFiles offset")
	}
	payload := data[pFiles:]
	var paths []string

	if fWide != 0 {
		var cur []uint16
		for i := 0; i+1 < len(payload); i += 2 {
			ch := binary.LittleEndian.Uint16(payload[i : i+2])
			if ch == 0 {
				if len(cur) == 0 {
					break
				}
				paths = append(paths, string(utf16.Decode(cur)))
				cur = cur[:0]
			} else {
				cur = append(cur, ch)
			}
		}
		return paths, nil
	}

	var cur []byte
	for _, b := range payload {
		if b == 0 {
			if len(cur) == 0 {
				break
			}
			paths = append(paths, string(cur))
			cur = cur[:0]
		} else {
			cur = append(cur, b)
		}
	}
	return paths, nil
}
