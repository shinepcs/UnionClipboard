//go:build windows

package platform

import (
	"bytes"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"unionclip/internal/clipboard"
	"unionclip/internal/model"
)

var (
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	cfHDROP       = 15
	gmemMoveable  = 0x0002
)

var (
	cfPNG             uint32
	cfExcludeMonitor  uint32
	cfCanIncludeHist  uint32
)

func initClipboardFormats() {
	if cfPNG == 0 {
		cfPNG = regClipFmt("PNG")
		cfExcludeMonitor = regClipFmt("ExcludeClipboardContentFromMonitorProcessing")
		cfCanIncludeHist = regClipFmt("CanIncludeInClipboardHistory")
	}
}

func regClipFmt(name string) uint32 {
	p, _ := windows.UTF16PtrFromString(name)
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(r)
}

func openClipboardWithRetry(hwnd uintptr) bool {
	for i := 0; i < 6; i++ {
		r, _, _ := procOpenClipboard.Call(hwnd)
		if r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func isFormatAvailable(fmtID uint32) bool {
	if fmtID == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(uintptr(fmtID))
	return r != 0
}

func foregroundExeName() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, 512)
	sz := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &sz); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:sz]))
}

func (p *windowsPlatform) ReadClipboard() (ClipboardSnapshot, bool) {
	initClipboardFormats()
	var snap ClipboardSnapshot
	snap.Foreground = foregroundExeName()

	if !openClipboardWithRetry(p.hwnd) {
		return snap, false
	}
	defer procCloseClipboard.Call()

	if isFormatAvailable(cfExcludeMonitor) {
		snap.Sensitive = true
	}
	if isFormatAvailable(cfCanIncludeHist) {
		if b := readClipboardBytes(cfCanIncludeHist); len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
			snap.Sensitive = true
		}
	}

	// Priority 1: CF_HDROP (Files / Folders)
	if isFormatAvailable(cfHDROP) {
		h, _, _ := procGetClipboardData.Call(uintptr(cfHDROP))
		if h != 0 {
			cnt, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
			if cnt > 0 && cnt < 4096 {
				var paths []string
				for i := uintptr(0); i < cnt; i++ {
					ln, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
					buf := make([]uint16, ln+1)
					procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), ln+1)
					paths = append(paths, windows.UTF16ToString(buf))
				}
				if len(paths) > 0 {
					snap.Kind = model.KindFiles
					snap.FilePaths = paths
					return snap, true
				}
			}
		}
	}

	// Priority 2: PNG or CF_DIB
	if isFormatAvailable(cfPNG) {
		if raw := readClipboardBytes(cfPNG); len(raw) > 0 {
			snap.Kind = model.KindImage
			snap.PNG = raw
			return snap, true
		}
	}
	if isFormatAvailable(cfDIB) {
		if raw := readClipboardBytes(cfDIB); len(raw) > 0 {
			if pngBytes, err := clipboard.DIBToPNG(raw); err == nil {
				snap.Kind = model.KindImage
				snap.PNG = pngBytes
				return snap, true
			}
		}
	}

	// Priority 3: CF_UNICODETEXT
	if isFormatAvailable(cfUnicodeText) {
		if raw := readClipboardBytes(cfUnicodeText); len(raw) >= 2 {
			u16 := unsafe.Slice((*uint16)(unsafe.Pointer(&raw[0])), len(raw)/2)
			s := windows.UTF16ToString(u16)
			if s != "" {
				snap.Kind = model.KindText
				snap.Text = s
				return snap, true
			}
		}
	}

	return snap, false
}

func (p *windowsPlatform) WriteClipboard(pl ClipboardPayload) error {
	initClipboardFormats()
	if !openClipboardWithRetry(p.hwnd) {
		return windows.ERROR_ACCESS_DENIED
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	switch pl.Kind {
	case model.KindText:
		u16, err := windows.UTF16FromString(pl.Text)
		if err != nil {
			return err
		}
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&u16[0])), len(u16)*2)
		setClipboardBytes(cfUnicodeText, raw)
	case model.KindImage:
		if len(pl.PNG) > 0 {
			setClipboardBytes(cfPNG, pl.PNG)
			if dib, err := clipboard.PNGToDIB(pl.PNG); err == nil {
				setClipboardBytes(cfDIB, dib)
			}
		}
	case model.KindFiles:
		if len(pl.FilePaths) > 0 {
			hdrop, err := clipboard.EncodeHDROP(pl.FilePaths)
			if err != nil {
				return err
			}
			setClipboardBytes(cfHDROP, hdrop)
		}
	}
	return nil
}

func readClipboardBytes(fmtID uint32) []byte {
	h, _, _ := procGetClipboardData.Call(uintptr(fmtID))
	if h == 0 {
		return nil
	}
	sz, _, _ := procGlobalSize.Call(h)
	if sz == 0 {
		return nil
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(h)
	out := make([]byte, sz)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), sz))
	return out
}

func setClipboardBytes(fmtID uint32, data []byte) {
	if fmtID == 0 || len(data) == 0 {
		return
	}
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data)), data)
	procGlobalUnlock.Call(h)
	r, _, _ := procSetClipboardData.Call(uintptr(fmtID), h)
	if r == 0 {
		_ = bytes.MinRead
	}
}
