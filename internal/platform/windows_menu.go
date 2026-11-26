//go:build windows

package platform

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procInsertMenuItemW  = user32.NewProc("InsertMenuItemW")
	procTrackPopupMenuEx = user32.NewProc("TrackPopupMenuEx")

	procCreateDIBSection = gdi32.NewProc("CreateDIBSection")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
)

const (
	miimState   = 0x00000001
	miimID      = 0x00000002
	miimSubMenu = 0x00000004
	miimFType   = 0x00000100
	miimString  = 0x00000040
	miimBitmap  = 0x00000080

	mftSeparator = 0x00000800
	mfsDisabled  = 0x00000003
	mfsChecked   = 0x00000008

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
)

type menuItemInfoW struct {
	cbSize        uint32
	fMask         uint32
	fType         uint32
	fState        uint32
	wID           uint32
	hSubMenu      uintptr
	hbmpChecked   uintptr
	hbmpUnchecked uintptr
	dwItemData    uintptr
	dwTypeData    *uint16
	cch           uint32
	hbmpItem      uintptr
}

type guiThreadInfo struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       rect
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

func (p *windowsPlatform) showWinPopupOnUIThread() {
	p.mu.Lock()
	menu := p.pendingPopup
	p.pendingPopup = nil
	cb := p.cb
	p.mu.Unlock()
	if menu == nil {
		return
	}

	prev, _, _ := procGetForegroundWindow.Call()
	p.mu.Lock()
	p.prevHwnd = prev
	p.mu.Unlock()

	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	var bitmaps []uintptr
	defer func() {
		procDestroyMenu.Call(hMenu)
		for _, bmp := range bitmaps {
			if bmp != 0 {
				procDeleteObject.Call(bmp)
			}
		}
	}()

	actions := make(map[uint32]func())
	var nextID uint32 = 1000
	pos := uint32(0)

	addItem := func(targetMenu uintptr, idxPos *uint32, title string, thumb []byte, checked, enabled bool, fn func()) {
		id := nextID
		nextID++
		if fn != nil {
			actions[id] = fn
		}
		ptr, _ := windows.UTF16PtrFromString(title)
		var mii menuItemInfoW
		mii.cbSize = uint32(unsafe.Sizeof(mii))
		mii.fMask = miimID | miimString | miimState
		mii.wID = id
		mii.dwTypeData = ptr
		if !enabled {
			mii.fState |= mfsDisabled
		}
		if checked {
			mii.fState |= mfsChecked
		}
		if len(thumb) > 0 {
			if hBmp := pngToHBitmap(thumb, 20, 20); hBmp != 0 {
				mii.fMask |= miimBitmap
				mii.hbmpItem = hBmp
				bitmaps = append(bitmaps, hBmp)
			}
		}
		procInsertMenuItemW.Call(targetMenu, uintptr(*idxPos), 1, uintptr(unsafe.Pointer(&mii)))
		*idxPos++
	}

	addSep := func(targetMenu uintptr, idxPos *uint32) {
		var mii menuItemInfoW
		mii.cbSize = uint32(unsafe.Sizeof(mii))
		mii.fMask = miimFType
		mii.fType = mftSeparator
		procInsertMenuItemW.Call(targetMenu, uintptr(*idxPos), 1, uintptr(unsafe.Pointer(&mii)))
		*idxPos++
	}

	if len(menu.History) == 0 {
		addItem(hMenu, &pos, "(클립보드 히스토리가 비어 있습니다)", nil, false, false, nil)
	} else {
		for idx, it := range menu.History {
			itemID := it.ID
			label := it.Label
			if k := HistoryShortcutKey(idx); k != "" {
				label = fmt.Sprintf("&%s  %s", k, it.Label)
			}
			addItem(hMenu, &pos, label, it.ThumbPNG, false, true, func() {
				if cb.OnMenuSelectItem != nil {
					cb.OnMenuSelectItem(itemID)
				}
			})
		}
	}

	if len(menu.Templates) > 0 || len(menu.TemplateFolders) > 0 {
		addSep(hMenu, &pos)
		hSub, _, _ := procCreatePopupMenu.Call()
		subPos := uint32(0)
		subIdx := 0
		for _, f := range menu.TemplateFolders {
			fk := SubmenuShortcutKey(subIdx)
			subIdx++
			hFolder, _, _ := procCreatePopupMenu.Call()
			fPos := uint32(0)
			for i, it := range f.Items {
				tid := it.ID
				lbl := it.Label
				if ik := SubmenuShortcutKey(i); ik != "" {
					lbl = fmt.Sprintf("&%s  %s", ik, it.Label)
				}
				addItem(hFolder, &fPos, lbl, it.ThumbPNG, false, true, func() {
					if cb.OnMenuSelectTmpl != nil {
						cb.OnMenuSelectTmpl(tid)
					}
				})
			}
			fTitle := f.Title
			if fk != "" {
				fTitle = fmt.Sprintf("&%s  %s", fk, f.Title)
			}
			fPtr, _ := windows.UTF16PtrFromString(fTitle)
			var fMii menuItemInfoW
			fMii.cbSize = uint32(unsafe.Sizeof(fMii))
			fMii.fMask = miimString | miimSubMenu
			fMii.hSubMenu = hFolder
			fMii.dwTypeData = fPtr
			procInsertMenuItemW.Call(hSub, uintptr(subPos), 1, uintptr(unsafe.Pointer(&fMii)))
			subPos++
		}
		for _, it := range menu.Templates {
			tk := SubmenuShortcutKey(subIdx)
			subIdx++
			tid := it.ID
			lbl := it.Label
			if tk != "" {
				lbl = fmt.Sprintf("&%s  %s", tk, it.Label)
			}
			addItem(hSub, &subPos, lbl, it.ThumbPNG, false, true, func() {
				if cb.OnMenuSelectTmpl != nil {
					cb.OnMenuSelectTmpl(tid)
				}
			})
		}
		tPtr, _ := windows.UTF16PtrFromString("&T  템플릿")
		var tMii menuItemInfoW
		tMii.cbSize = uint32(unsafe.Sizeof(tMii))
		tMii.fMask = miimString | miimSubMenu
		tMii.hSubMenu = hSub
		tMii.dwTypeData = tPtr
		procInsertMenuItemW.Call(hMenu, uintptr(pos), 1, uintptr(unsafe.Pointer(&tMii)))
		pos++
	}

	addSep(hMenu, &pos)
	addItem(hMenu, &pos, "유니버설 클립보드 (자동 반영)", nil, menu.UniversalClipboard, true, func() {
		if cb.OnToggleUniversal != nil {
			cb.OnToggleUniversal()
		}
	})
	addItem(hMenu, &pos, "뷰어 · 설정 열기...", nil, false, true, func() {
		if cb.OnOpenViewer != nil {
			cb.OnOpenViewer()
		}
	})

	x, y := popupCoordinates(prev)
	procSetForegroundWindow.Call(p.hwnd)
	cmd, _, _ := procTrackPopupMenuEx.Call(
		hMenu,
		tpmReturnCmd|tpmRightButton|tpmNonotify,
		uintptr(x), uintptr(y),
		p.hwnd, 0,
	)
	procPostMessageW.Call(p.hwnd, wmNull, 0, 0)

	if cmd != 0 {
		if fn, ok := actions[uint32(cmd)]; ok && fn != nil {
			go fn()
		}
	}
}

func (p *windowsPlatform) showTrayContextMenuOnUIThread() {
	p.mu.Lock()
	cb := p.cb
	st := p.trayState
	p.mu.Unlock()

	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	actions := make(map[uint32]func())
	var nextID uint32 = 2000
	pos := uint32(0)

	add := func(title string, checked, enabled bool, fn func()) {
		id := nextID
		nextID++
		if fn != nil {
			actions[id] = fn
		}
		ptr, _ := windows.UTF16PtrFromString(title)
		var mii menuItemInfoW
		mii.cbSize = uint32(unsafe.Sizeof(mii))
		mii.fMask = miimID | miimString | miimState
		mii.wID = id
		mii.dwTypeData = ptr
		if !enabled {
			mii.fState |= mfsDisabled
		}
		if checked {
			mii.fState |= mfsChecked
		}
		procInsertMenuItemW.Call(hMenu, uintptr(pos), 1, uintptr(unsafe.Pointer(&mii)))
		pos++
	}
	sep := func() {
		var mii menuItemInfoW
		mii.cbSize = uint32(unsafe.Sizeof(mii))
		mii.fMask = miimFType
		mii.fType = mftSeparator
		procInsertMenuItemW.Call(hMenu, uintptr(pos), 1, uintptr(unsafe.Pointer(&mii)))
		pos++
	}

	add(fmt.Sprintf("연결된 기기: %d / %d", st.OnlinePeers, st.TotalPeers), false, false, nil)
	sep()
	add(fmt.Sprintf("클립보드 히스토리 열기 (%s)", st.Hotkey), false, true, func() {
		if cb.OnHotkey != nil {
			cb.OnHotkey()
		}
	})
	add("히스토리 · 템플릿 · 기기 관리...", false, true, func() {
		if cb.OnOpenViewer != nil {
			cb.OnOpenViewer()
		}
	})
	sep()
	add("유니버설 클립보드", st.UniversalClipboard, true, func() {
		if cb.OnToggleUniversal != nil {
			cb.OnToggleUniversal()
		}
	})
	add("캡처 일시중지", st.Paused, true, func() {
		if cb.OnTogglePause != nil {
			cb.OnTogglePause()
		}
	})
	sep()
	add("종료", false, true, func() {
		if cb.OnQuit != nil {
			cb.OnQuit()
		}
	})

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(p.hwnd)
	cmd, _, _ := procTrackPopupMenuEx.Call(hMenu, tpmReturnCmd|tpmRightButton|tpmNonotify, uintptr(pt.X), uintptr(pt.Y), p.hwnd, 0)
	procPostMessageW.Call(p.hwnd, wmNull, 0, 0)
	if cmd != 0 {
		if fn, ok := actions[uint32(cmd)]; ok {
			go fn()
		}
	}
}

func popupCoordinates(prevHwnd uintptr) (int32, int32) {
	if prevHwnd != 0 {
		tid, _, _ := procGetWindowThreadProcessId.Call(prevHwnd, 0)
		if tid != 0 {
			var gti guiThreadInfo
			gti.cbSize = uint32(unsafe.Sizeof(gti))
			r, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti)))
			if r != 0 && gti.hwndCaret != 0 {
				pt := point{X: gti.rcCaret.Left, Y: gti.rcCaret.Bottom}
				procClientToScreen.Call(gti.hwndCaret, uintptr(unsafe.Pointer(&pt)))
				if pt.X != 0 || pt.Y != 0 {
					return pt.X, pt.Y
				}
			}
		}
	}
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return pt.X, pt.Y
}

func pngToHBitmap(pngBytes []byte, w, h int) uintptr {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return 0
	}
	bmi := bitmapInfoHeader{
		biSize:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:    int32(w),
		biHeight:   -int32(h), // top-down
		biPlanes:   1,
		biBitCount: 32,
	}
	var bitsPtr unsafe.Pointer
	hBmp, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bitsPtr)), 0, 0)
	if hBmp == 0 || bitsPtr == nil {
		return 0
	}
	buf := unsafe.Slice((*byte)(bitsPtr), w*h*4)
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		sy := b.Min.Y + (y*sh)/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + (x*sw)/w
			c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
			off := (y*w + x) * 4
			buf[off+0] = byte((uint16(c.B) * uint16(c.A)) / 255)
			buf[off+1] = byte((uint16(c.G) * uint16(c.A)) / 255)
			buf[off+2] = byte((uint16(c.R) * uint16(c.A)) / 255)
			buf[off+3] = c.A
		}
	}
	_ = image.Rect(0, 0, w, h)
	return hBmp
}
