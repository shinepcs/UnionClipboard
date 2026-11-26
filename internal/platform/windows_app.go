//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW          = user32.NewProc("RegisterClassExW")
	procCreateWindowExW           = user32.NewProc("CreateWindowExW")
	procDefWindowProcW            = user32.NewProc("DefWindowProcW")
	procGetMessageW               = user32.NewProc("GetMessageW")
	procTranslateMessage          = user32.NewProc("TranslateMessage")
	procDispatchMessageW          = user32.NewProc("DispatchMessageW")
	procPostMessageW              = user32.NewProc("PostMessageW")
	procSendMessageW              = user32.NewProc("SendMessageW")
	procPostQuitMessage           = user32.NewProc("PostQuitMessage")
	procRegisterHotKey            = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey          = user32.NewProc("UnregisterHotKey")
	procAddClipboardFormatListener = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	procGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	procGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo          = user32.NewProc("GetGUIThreadInfo")
	procClientToScreen            = user32.NewProc("ClientToScreen")
	procGetCursorPos              = user32.NewProc("GetCursorPos")
	procSendInput                 = user32.NewProc("SendInput")
	procLoadIconW                 = user32.NewProc("LoadIconW")
	procDestroyWindow             = user32.NewProc("DestroyWindow")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procDragQueryFileW   = shell32.NewProc("DragQueryFileW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull            = 0x0000
	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmApp             = 0x8000
	wmTrayIcon        = wmApp + 1
	wmShowPopup       = wmApp + 2
	wmUpdateTray      = wmApp + 3
	wmRegisterHotkey  = wmApp + 4
	wmSendPaste       = wmApp + 5
	wmHotKey          = 0x0312
	wmClipboardUpdate = 0x031D
	wmLButtonUp       = 0x0202
	wmRButtonUp       = 0x0205
	wmLButtonDblClk   = 0x0203

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	inputKeyboard  = 1
	keyeventfKeyup = 0x0002
	vkControl      = 0x11
	vkV            = 0x56
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct {
	X int32
	Y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

// 64-bit Windows INPUT struct = 40 bytes (4 + 4 pad + 32 union)
type winInput struct {
	Type uint32
	_    uint32
	Vk   uint16
	Scan uint16
	Flags uint32
	Time uint32
	_    uint32
	Extra uintptr
	_    [8]byte // union padding to match MOUSEINPUT 32-byte size
}

type windowsPlatform struct {
	mu           sync.Mutex
	cb           Callbacks
	hwnd         uintptr
	prevHwnd     uintptr
	trayState    TrayState
	pendingPopup *PopupMenuModel
	pendingHK    HotkeySpec
	readyCh      chan error
}

var activeWin *windowsPlatform

func New() Platform {
	p := &windowsPlatform{
		readyCh: make(chan error, 1),
	}
	activeWin = p
	return p
}

func (p *windowsPlatform) Run(cb Callbacks) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	p.mu.Lock()
	p.cb = cb
	p.mu.Unlock()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	clsName, _ := windows.UTF16PtrFromString("UnionClipboardMsgWindow")
	wndName, _ := windows.UTF16PtrFromString("UnionClipboard")
	icon, _, _ := procLoadIconW.Call(0, uintptr(32512)) // IDI_APPLICATION

	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(winWndProc),
		hInstance:     hInst,
		hIcon:         icon,
		lpszClassName: clsName,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(wndName)),
		0, 0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		p.readyCh <- fmt.Errorf("CreateWindowExW: %v", err)
		return err
	}
	p.hwnd = hwnd

	procAddClipboardFormatListener.Call(hwnd)
	p.addTrayIcon(icon)
	p.readyCh <- nil

	p.mu.Lock()
	onReady := p.cb.OnReady
	p.mu.Unlock()
	if onReady != nil {
		go onReady()
	}

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func (p *windowsPlatform) Quit() {
	if p.hwnd != 0 {
		procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
	}
}

func (p *windowsPlatform) RegisterHotkey(spec string) error {
	hk, err := ParseHotkey(spec)
	if err != nil {
		return err
	}
	if windowsVirtualKey(hk.Key) == 0 {
		return fmt.Errorf("Windows에서 지원하지 않는 키입니다: %s", hk.Key)
	}
	p.mu.Lock()
	p.pendingHK = hk
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd != 0 {
		r, _, _ := procSendMessageW.Call(hwnd, wmRegisterHotkey, 0, 0)
		if r == 0 {
			return fmt.Errorf("Windows 단축키 %q 등록에 실패했습니다 (다른 앱과 충돌할 수 있습니다)", spec)
		}
	}
	return nil
}

func (p *windowsPlatform) applyHotkeyOnUIThread() uintptr {
	p.mu.Lock()
	hk := p.pendingHK
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd == 0 {
		return 0
	}
	procUnregisterHotKey.Call(hwnd, 1)
	var mods uint32 = modNoRepeat
	if hk.Ctrl {
		mods |= modControl
	}
	if hk.Alt {
		mods |= modAlt
	}
	if hk.Shift {
		mods |= modShift
	}
	if hk.Cmd {
		mods |= modWin
	}
	vk := windowsVirtualKey(hk.Key)
	r, _, _ := procRegisterHotKey.Call(hwnd, 1, uintptr(mods), uintptr(vk))
	return r
}

func windowsVirtualKey(key string) uint32 {
	if len(key) == 1 && ((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')) {
		return uint32(key[0])
	}
	if key == "Space" {
		return 0x20
	}
	if len(key) >= 2 && key[0] == 'F' {
		var n int
		if _, err := fmt.Sscanf(key[1:], "%d", &n); err == nil && n >= 1 && n <= 12 {
			return uint32(0x70 + n - 1)
		}
	}
	return 0
}

func (p *windowsPlatform) ShowPopupMenu(menu PopupMenuModel) {
	p.mu.Lock()
	p.pendingPopup = &menu
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmShowPopup, 0, 0)
	}
}

func (p *windowsPlatform) SendPaste() {
	p.mu.Lock()
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmSendPaste, 0, 0)
	}
}

func (p *windowsPlatform) sendCtrlVOnUIThread() {
	p.mu.Lock()
	target := p.prevHwnd
	p.mu.Unlock()
	if target != 0 {
		procSetForegroundWindow.Call(target)
	}
	time.Sleep(60 * time.Millisecond)

	downs := [2]winInput{
		{Type: inputKeyboard, Vk: vkControl},
		{Type: inputKeyboard, Vk: vkV},
	}
	ups := [2]winInput{
		{Type: inputKeyboard, Vk: vkV, Flags: keyeventfKeyup},
		{Type: inputKeyboard, Vk: vkControl, Flags: keyeventfKeyup},
	}
	procSendInput.Call(2, uintptr(unsafe.Pointer(&downs[0])), unsafe.Sizeof(downs[0]))
	time.Sleep(15 * time.Millisecond)
	procSendInput.Call(2, uintptr(unsafe.Pointer(&ups[0])), unsafe.Sizeof(ups[0]))
}

func (p *windowsPlatform) OpenViewer(url string) {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, exe := range candidates {
		if exe != "" {
			if _, err := os.Stat(exe); err == nil {
				_ = exec.Command(exe, "--app="+url, "--window-size=1080,740").Start()
				return
			}
		}
	}
	if exe, err := exec.LookPath("msedge.exe"); err == nil {
		_ = exec.Command(exe, "--app="+url, "--window-size=1080,740").Start()
		return
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func (p *windowsPlatform) UpdateTray(state TrayState) {
	p.mu.Lock()
	p.trayState = state
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmUpdateTray, 0, 0)
	}
}

func (p *windowsPlatform) ShowNotification(title, body string) {
	p.mu.Lock()
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifInfo
	copyUTF16(nid.szInfoTitle[:], title)
	copyUTF16(nid.szInfo[:], body)
	nid.dwInfoFlags = 1
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (p *windowsPlatform) HasAccessibilityPermission() bool {
	return true
}

func (p *windowsPlatform) RequestAccessibilityPermission() {}

func (p *windowsPlatform) RevealPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return exec.Command("explorer.exe", "/select,"+paths[0]).Start()
}

func (p *windowsPlatform) addTrayIcon(icon uintptr) {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = p.hwnd
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTrayIcon
	nid.hIcon = icon
	copyUTF16(nid.szTip[:], "UnionClipboard")
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func (p *windowsPlatform) refreshTrayTip() {
	p.mu.Lock()
	st := p.trayState
	p.mu.Unlock()
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = p.hwnd
	nid.uID = 1
	nid.uFlags = nifTip
	tip := fmt.Sprintf("UnionClipboard (%d/%d 기기)", st.OnlinePeers, st.TotalPeers)
	copyUTF16(nid.szTip[:], tip)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (p *windowsPlatform) removeTrayIcon() {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = p.hwnd
	nid.uID = 1
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func winWndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	p := activeWin
	if p == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
		return ret
	}
	switch uMsg {
	case wmHotKey:
		p.mu.Lock()
		fn := p.cb.OnHotkey
		p.mu.Unlock()
		if fn != nil {
			go fn()
		}
		return 0
	case wmClipboardUpdate:
		if snap, ok := p.ReadClipboard(); ok {
			p.mu.Lock()
			fn := p.cb.OnClipboardChange
			p.mu.Unlock()
			if fn != nil {
				go fn(snap)
			}
		}
		return 0
	case wmShowPopup:
		p.showWinPopupOnUIThread()
		return 0
	case wmRegisterHotkey:
		return p.applyHotkeyOnUIThread()
	case wmUpdateTray:
		p.refreshTrayTip()
		return 0
	case wmSendPaste:
		p.sendCtrlVOnUIThread()
		return 0
	case wmTrayIcon:
		switch lParam {
		case wmLButtonUp, wmLButtonDblClk:
			p.mu.Lock()
			fn := p.cb.OnOpenViewer
			p.mu.Unlock()
			if fn != nil {
				go fn()
			}
		case wmRButtonUp:
			p.showTrayContextMenuOnUIThread()
		}
		return 0
	case wmClose:
		procRemoveClipboardFormatListener.Call(hwnd)
		procUnregisterHotKey.Call(hwnd, 1)
		p.removeTrayIcon()
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
}

func copyUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	n := len(dst) - 1
	if len(u) < n {
		n = len(u)
	}
	copy(dst[:n], u[:n])
	dst[n] = 0
}
