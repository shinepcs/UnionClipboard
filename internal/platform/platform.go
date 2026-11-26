package platform

import (
	"fmt"
	"strconv"
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
	Alt   bool   // Option on macOS, Alt on Windows
	Shift bool
	Cmd   bool   // Command on macOS, Win on Windows
	Key   string // "A"-"Z", "0"-"9", "Space", "F1"-"F12"
}

func DefaultHotkeyForOS(goos string) string {
	if goos == "darwin" {
		return "Option+V"
	}
	return "Alt+V"
}

func ParseHotkey(spec string) (HotkeySpec, error) {
	var out HotkeySpec
	parts := strings.Split(strings.TrimSpace(spec), "+")
	if len(parts) < 2 {
		return out, fmt.Errorf("단축키 형식 오류 %q (예: Option+V, Alt+V, Ctrl+Shift+V)", spec)
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
			return out, fmt.Errorf("알 수 없는 보조키 %q", raw)
		}
	}
	if !out.Ctrl && !out.Alt && !out.Shift && !out.Cmd {
		return out, fmt.Errorf("최소 1개 이상의 보조키(Option/Alt/Ctrl/Shift/Cmd)가 필요합니다")
	}
	keyPart := strings.ToUpper(strings.TrimSpace(parts[len(parts)-1]))
	switch {
	case len(keyPart) == 1 && ((keyPart[0] >= 'A' && keyPart[0] <= 'Z') || (keyPart[0] >= '0' && keyPart[0] <= '9')):
		out.Key = keyPart
	case keyPart == "SPACE" || keyPart == "SPACEBAR":
		out.Key = "Space"
	case strings.HasPrefix(keyPart, "F") && len(keyPart) >= 2 && len(keyPart) <= 3:
		n, err := strconv.Atoi(keyPart[1:])
		if err != nil || n < 1 || n > 12 {
			return out, fmt.Errorf("지원하지 않는 키 %q", keyPart)
		}
		out.Key = fmt.Sprintf("F%d", n)
	default:
		return out, fmt.Errorf("지원하지 않는 키 %q (A-Z, 0-9, Space, F1-F12 지원)", keyPart)
	}
	return out, nil
}

func FormatHotkey(hk HotkeySpec, goos string) string {
	var parts []string
	if hk.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if hk.Alt {
		if goos == "darwin" {
			parts = append(parts, "Option")
		} else {
			parts = append(parts, "Alt")
		}
	}
	if hk.Shift {
		parts = append(parts, "Shift")
	}
	if hk.Cmd {
		if goos == "darwin" {
			parts = append(parts, "Cmd")
		} else {
			parts = append(parts, "Win")
		}
	}
	parts = append(parts, hk.Key)
	return strings.Join(parts, "+")
}

// HistoryShortcutKey returns 1..9, 0, A..S, U..Z (reserving T exclusively for Templates).
func HistoryShortcutKey(idx int) string {
	if idx < 0 {
		return ""
	}
	if idx < 9 {
		return fmt.Sprintf("%d", idx+1)
	}
	if idx == 9 {
		return "0"
	}
	alphaIdx := idx - 10
	const letters = "ABCDEFGHIJKLMNOPQRSUVWXYZ" // 'T' skipped
	if alphaIdx < len(letters) {
		return letters[alphaIdx : alphaIdx+1]
	}
	return ""
}

// SubmenuShortcutKey returns 1..9, 0, A..Z for items inside the Templates submenu hierarchy.
func SubmenuShortcutKey(idx int) string {
	if idx < 0 {
		return ""
	}
	if idx < 9 {
		return fmt.Sprintf("%d", idx+1)
	}
	if idx == 9 {
		return "0"
	}
	alphaIdx := idx - 10
	if alphaIdx < 26 {
		return string(rune('A' + alphaIdx))
	}
	return ""
}
