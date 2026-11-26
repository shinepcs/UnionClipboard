//go:build darwin

package platform

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework WebKit -framework ApplicationServices -framework UserNotifications
#include <stdlib.h>
#include "darwin_bridge_darwin.h"
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"unionclip/internal/model"
)

type darwinPlatform struct {
	mu          sync.Mutex
	cb          Callbacks
	tagActions  map[int]func()
}

var activeDarwin *darwinPlatform

func New() Platform {
	p := &darwinPlatform{
		tagActions: make(map[int]func()),
	}
	activeDarwin = p
	return p
}

func (p *darwinPlatform) Run(cb Callbacks) error {
	p.mu.Lock()
	p.cb = cb
	p.mu.Unlock()
	C.uc_run_app()
	return nil
}

func (p *darwinPlatform) Quit() {
	C.uc_quit_app()
}

func (p *darwinPlatform) RegisterHotkey(spec string) error {
	hk, err := ParseHotkey(spec)
	if err != nil {
		return err
	}
	ok := C.uc_register_hotkey(C.bool(hk.Ctrl), C.bool(hk.Alt), C.bool(hk.Shift), C.bool(hk.Cmd), C.uint8_t(hk.Key))
	if !ok {
		return fmt.Errorf("failed to register macOS hotkey %q", spec)
	}
	return nil
}

func (p *darwinPlatform) ReadClipboard() (ClipboardSnapshot, bool) {
	// Capture triggers goOnClipboardChange synchronously if needed
	return ClipboardSnapshot{}, false
}

func (p *darwinPlatform) WriteClipboard(pl ClipboardPayload) error {
	switch pl.Kind {
	case model.KindText:
		cs := C.CString(pl.Text)
		defer C.free(unsafe.Pointer(cs))
		C.uc_write_clipboard_text(cs)
	case model.KindImage:
		if len(pl.PNG) > 0 {
			C.uc_write_clipboard_png(unsafe.Pointer(&pl.PNG[0]), C.int(len(pl.PNG)))
		}
	case model.KindFiles:
		if len(pl.FilePaths) == 0 {
			return nil
		}
		cPaths := make([]*C.char, len(pl.FilePaths))
		for i, fp := range pl.FilePaths {
			cPaths[i] = C.CString(fp)
		}
		defer func() {
			for _, cp := range cPaths {
				C.free(unsafe.Pointer(cp))
			}
		}()
		C.uc_write_clipboard_files(&cPaths[0], C.int(len(cPaths)))
	}
	return nil
}

func (p *darwinPlatform) ShowPopupMenu(menu PopupMenuModel) {
	p.mu.Lock()
	p.tagActions = make(map[int]func())
	nextTag := 100
	addAction := func(fn func()) int {
		t := nextTag
		nextTag++
		p.tagActions[t] = fn
		return t
	}
	cb := p.cb
	p.mu.Unlock()

	C.uc_popup_reset()

	addMenuItem := func(tag int, title string, thumb []byte, keyEquiv string, enabled bool) {
		ct := C.CString(title)
		ck := C.CString(keyEquiv)
		var thumbPtr unsafe.Pointer
		if len(thumb) > 0 {
			thumbPtr = unsafe.Pointer(&thumb[0])
		}
		C.uc_popup_add_item(C.int(tag), ct, thumbPtr, C.int(len(thumb)), ck, C.bool(enabled))
		C.free(unsafe.Pointer(ct))
		C.free(unsafe.Pointer(ck))
	}

	if len(menu.History) == 0 {
		addMenuItem(0, "(클립보드 히스토리가 비어 있습니다)", nil, "", false)
	} else {
		for idx, it := range menu.History {
			key := keyEquivForIndex(idx)
			itemID := it.ID
			tag := addAction(func() {
				if cb.OnMenuSelectItem != nil {
					cb.OnMenuSelectItem(itemID)
				}
			})
			prefix := ""
			if key != "" {
				prefix = strings.ToUpper(key) + "  "
			}
			addMenuItem(tag, prefix+it.Label, it.ThumbPNG, key, true)
		}
	}

	if len(menu.Templates) > 0 || len(menu.TemplateFolders) > 0 {
		C.uc_popup_add_separator()
		cSub := C.CString("템플릿")
		C.uc_popup_begin_submenu(cSub)
		C.free(unsafe.Pointer(cSub))

		for _, f := range menu.TemplateFolders {
			cf := C.CString(f.Title)
			C.uc_popup_begin_submenu(cf)
			C.free(unsafe.Pointer(cf))
			for _, it := range f.Items {
				tid := it.ID
				tag := addAction(func() {
					if cb.OnMenuSelectTmpl != nil {
						cb.OnMenuSelectTmpl(tid)
					}
				})
				addMenuItem(tag, it.Label, it.ThumbPNG, "", true)
			}
			C.uc_popup_end_submenu()
		}
		for _, it := range menu.Templates {
			tid := it.ID
			tag := addAction(func() {
				if cb.OnMenuSelectTmpl != nil {
					cb.OnMenuSelectTmpl(tid)
				}
			})
			addMenuItem(tag, it.Label, it.ThumbPNG, "", true)
		}
		C.uc_popup_end_submenu()
	}

	C.uc_popup_add_separator()
	univLabel := "유니버설 클립보드: 꺼짐"
	if menu.UniversalClipboard {
		univLabel = "✓ 유니버설 클립보드: 켜짐"
	}
	addMenuItem(addAction(func() {
		if cb.OnToggleUniversal != nil {
			cb.OnToggleUniversal()
		}
	}), univLabel, nil, "", true)

	addMenuItem(addAction(func() {
		if cb.OnOpenViewer != nil {
			cb.OnOpenViewer()
		}
	}), "뷰어 · 설정 열기...", nil, "", true)

	C.uc_popup_show()
}

func keyEquivForIndex(idx int) string {
	if idx < 9 {
		return fmt.Sprintf("%d", idx+1)
	}
	if idx == 9 {
		return "0"
	}
	if idx < 36 {
		return string(rune('a' + (idx - 10)))
	}
	return ""
}

func (p *darwinPlatform) SendPaste() {
	C.uc_send_paste()
}

func (p *darwinPlatform) OpenViewer(url string) {
	cu := C.CString(url)
	defer C.free(unsafe.Pointer(cu))
	C.uc_open_viewer(cu)
}

func (p *darwinPlatform) UpdateTray(state TrayState) {
	ch := C.CString(state.Hotkey)
	defer C.free(unsafe.Pointer(ch))
	C.uc_update_tray(C.int(state.OnlinePeers), C.int(state.TotalPeers), C.bool(state.UniversalClipboard), C.bool(state.Paused), ch)
}

func (p *darwinPlatform) ShowNotification(title, body string) {
	ct := C.CString(title)
	cb := C.CString(body)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cb))
	C.uc_show_notification(ct, cb)
}

func (p *darwinPlatform) HasAccessibilityPermission() bool {
	return bool(C.uc_has_accessibility())
}

func (p *darwinPlatform) RequestAccessibilityPermission() {
	C.uc_request_accessibility()
}

func (p *darwinPlatform) RevealPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cPaths := make([]*C.char, len(paths))
	for i, fp := range paths {
		cPaths[i] = C.CString(fp)
	}
	defer func() {
		for _, cp := range cPaths {
			C.free(unsafe.Pointer(cp))
		}
	}()
	C.uc_reveal_paths(&cPaths[0], C.int(len(cPaths)))
	return nil
}

//export goOnReady
func goOnReady() {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnReady
	activeDarwin.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

//export goOnClipboardChange
func goOnClipboardChange(kind C.int, text *C.char, pngBytes unsafe.Pointer, pngLen C.int, pathsJoined *C.char, fgBundle *C.char, sensitive C.int) {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnClipboardChange
	activeDarwin.mu.Unlock()
	if fn == nil {
		return
	}

	var snap ClipboardSnapshot
	snap.Foreground = C.GoString(fgBundle)
	snap.Sensitive = sensitive != 0
	switch int(kind) {
	case 1:
		snap.Kind = model.KindText
		snap.Text = C.GoString(text)
	case 2:
		snap.Kind = model.KindImage
		if pngBytes != nil && pngLen > 0 {
			snap.PNG = C.GoBytes(pngBytes, pngLen)
		}
	case 3:
		snap.Kind = model.KindFiles
		raw := C.GoString(pathsJoined)
		for _, line := range strings.Split(raw, "\n") {
			if strings.TrimSpace(line) != "" {
				snap.FilePaths = append(snap.FilePaths, line)
			}
		}
	default:
		return
	}
	go fn(snap)
}

//export goOnHotkey
func goOnHotkey() {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnHotkey
	activeDarwin.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

//export goOnPopupTag
func goOnPopupTag(tag C.int) {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	act := activeDarwin.tagActions[int(tag)]
	activeDarwin.mu.Unlock()
	if act != nil {
		go act()
	}
}

//export goOnOpenViewer
func goOnOpenViewer() {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnOpenViewer
	activeDarwin.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

//export goOnToggleUniversal
func goOnToggleUniversal() {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnToggleUniversal
	activeDarwin.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

//export goOnTogglePause
func goOnTogglePause() {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnTogglePause
	activeDarwin.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

//export goOnQuit
func goOnQuit() {
	if activeDarwin == nil {
		return
	}
	activeDarwin.mu.Lock()
	fn := activeDarwin.cb.OnQuit
	activeDarwin.mu.Unlock()
	if fn != nil {
		go fn()
	}
}
