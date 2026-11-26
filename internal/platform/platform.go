package platform

import (
	"fmt"
	"strings"

	"unionclip/internal/model"
)

// ClipboardSnapshot represents content captured from the OS clipboard.
type ClipboardSnapshot struct {
	Kind       model.ItemKind
	Text       string
	PNG        []byte
	FilePaths  []string
	Foreground string // Bundle ID on macOS, process executable name on Windows
	Sensitive  bool   // Marked concealed/transient by password managers
}

// ClipboardPayload represents content to write to the OS clipboard.
type ClipboardPayload struct {
	Kind      model.ItemKind
	Text      string
	PNG       []byte
	FilePaths []string
}

type PopupMenuItem struct {
	ID       string
	Label    string
	ThumbPNG []byte
	IsHeader bool
}

type PopupMenuFolder struct {
	Title string
	Items []PopupMenuItem
}

type PopupMenuModel struct {
	History            []PopupMenuItem
	Templates          []PopupMenuItem
	TemplateFolders    []PopupMenuFolder
	UniversalClipboard bool
	Paused             bool
	PeerCount          int
}

type TrayState struct {
	OnlinePeers        int
	TotalPeers         int
	UniversalClipboard bool
	Paused             bool
	Hotkey             string
}

type Callbacks struct {
	OnReady            func()
	OnClipboardChange  func(snap ClipboardSnapshot)
	OnHotkey           func()
	OnMenuSelectItem   func(itemID string)
	OnMenuSelectTmpl   func(tmplID string)
	OnOpenViewer       func()
	OnToggleUniversal  func()
	OnTogglePause      func()
	OnQuit             func()
}

type Platform interface {
	Run(cb Callbacks) error
	Quit()
	RegisterHotkey(spec string) error
	ReadClipboard() (ClipboardSnapshot, bool)
	WriteClipboard(p ClipboardPayload) error
	ShowPopupMenu(menu PopupMenuModel)
	SendPaste()
	OpenViewer(url string)
	UpdateTray(state TrayState)
	ShowNotification(title, body string)
	HasAccessibilityPermission() bool
	RequestAccessibilityPermission()
	RevealPaths(paths []string) error
}

type HotkeySpec struct {
	Ctrl  bool
	Alt   bool // Option on macOS, Alt on Windows
	Shift bool
	Cmd   bool // Command on macOS, Win on Windows
	Key   byte // ASCII uppercase letter 'A'-'Z' or '0'-'9'
}

func ParseHotkey(spec string) (HotkeySpec, error) {
	var out HotkeySpec
	parts := strings.Split(strings.TrimSpace(spec), "+")
	if len(parts) < 2 {
		return out, fmt.Errorf("invalid hotkey %q (expected e.g. Option+C or Alt+C)", spec)
	}
	for _, raw := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "ctrl", "control":
			out.Ctrl = true
		case "alt", "option", "opt":
			out.Alt = true
		case "shift":
			out.Shift = true
		case "cmd", "command", "win", "super":
			out.Cmd = true
		default:
			return out, fmt.Errorf("unknown modifier %q", raw)
		}
	}
	keyPart := strings.ToUpper(strings.TrimSpace(parts[len(parts)-1]))
	if len(keyPart) != 1 || !((keyPart[0] >= 'A' && keyPart[0] <= 'Z') || (keyPart[0] >= '0' && keyPart[0] <= '9')) {
		return out, fmt.Errorf("unsupported hotkey key %q", keyPart)
	}
	out.Key = keyPart[0]
	return out, nil
}
