package model

import (
	"os"
	"runtime"
)

type ItemKind string

const (
	KindText  ItemKind = "text"
	KindImage ItemKind = "image"
	KindFiles ItemKind = "files"
)

// FileEntry represents a copied file or directory entry.
// OrigPath is never sent to remote peers; LocalCachedPath is local to each machine.
type FileEntry struct {
	RelativeName    string `json:"relative_name"`
	OrigPath        string `json:"orig_path,omitempty"`
	Size            int64  `json:"size"`
	IsDir           bool   `json:"is_dir"`
	ModTime         int64  `json:"mod_time"`
	LocalCachedPath string `json:"local_cached_path,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
}

// ClipboardItem is a single entry in clipboard history.
type ClipboardItem struct {
	ID               string      `json:"id"`
	Kind             ItemKind    `json:"kind"`
	ContentHash      string      `json:"content_hash"`
	TextContent      string      `json:"text_content,omitempty"`
	Title            string      `json:"title"`
	BlobPath         string      `json:"blob_path,omitempty"`
	ThumbPath        string      `json:"thumb_path,omitempty"`
	ByteSize         int64       `json:"byte_size"`
	ImageWidth       int         `json:"image_width,omitempty"`
	ImageHeight      int         `json:"image_height,omitempty"`
	Files            []FileEntry `json:"files,omitempty"`
	OriginDeviceID   string      `json:"origin_device_id"`
	OriginDeviceName string      `json:"origin_device_name"`
	OriginOS         string      `json:"origin_os"`
	CreatedAt        int64       `json:"created_at"`
	UpdatedAt        int64       `json:"updated_at"`
	DeletedAt        int64       `json:"deleted_at,omitempty"`
}

// SanitizedForSync returns a copy safe to transmit to remote peers (strips local filesystem paths).
func (c ClipboardItem) SanitizedForSync() ClipboardItem {
	out := c
	out.BlobPath = ""
	out.ThumbPath = ""
	if len(c.Files) > 0 {
		out.Files = make([]FileEntry, len(c.Files))
		for i, f := range c.Files {
			f.OrigPath = ""
			f.LocalCachedPath = ""
			out.Files[i] = f
		}
	}
	return out
}

// NeedsRemoteFetch reports whether this file item belongs to a remote device and lacks local cached files.
func (c ClipboardItem) NeedsRemoteFetch(localDeviceID string) bool {
	if c.Kind != KindFiles || len(c.Files) == 0 {
		return false
	}
	if c.OriginDeviceID == localDeviceID {
		return false
	}
	for _, f := range c.Files {
		if f.LocalCachedPath == "" {
			return true
		}
		if _, err := os.Stat(f.LocalCachedPath); err != nil {
			return true
		}
	}
	return false
}

// LocalPaths returns existing local file paths for this item (OrigPath if local, else LocalCachedPath).
func (c ClipboardItem) LocalPaths(localDeviceID string) []string {
	paths := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		p := f.LocalCachedPath
		if c.OriginDeviceID == localDeviceID && f.OrigPath != "" {
			p = f.OrigPath
		}
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// TemplateFolder represents a folder in the template tree.
type TemplateFolder struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id,omitempty"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	UpdatedAt int64  `json:"updated_at"`
	DeletedAt int64  `json:"deleted_at,omitempty"`
}

// TemplateItem is a pinned/reusable clipboard item inside a template folder (or root).
type TemplateItem struct {
	ID             string      `json:"id"`
	FolderID       string      `json:"folder_id,omitempty"`
	Title          string      `json:"title"`
	Kind           ItemKind    `json:"kind"`
	ContentHash    string      `json:"content_hash"`
	TextContent    string      `json:"text_content,omitempty"`
	BlobPath       string      `json:"blob_path,omitempty"`
	ThumbPath      string      `json:"thumb_path,omitempty"`
	ByteSize       int64       `json:"byte_size"`
	ImageWidth     int         `json:"image_width,omitempty"`
	ImageHeight    int         `json:"image_height,omitempty"`
	Files          []FileEntry `json:"files,omitempty"`
	OriginDeviceID string      `json:"origin_device_id"`
	SortOrder      int         `json:"sort_order"`
	UpdatedAt      int64       `json:"updated_at"`
	DeletedAt      int64       `json:"deleted_at,omitempty"`
}

// SanitizedForSync returns a copy safe to transmit to remote peers.
func (t TemplateItem) SanitizedForSync() TemplateItem {
	out := t
	out.BlobPath = ""
	out.ThumbPath = ""
	if len(t.Files) > 0 {
		out.Files = make([]FileEntry, len(t.Files))
		for i, f := range t.Files {
			f.OrigPath = ""
			f.LocalCachedPath = ""
			out.Files[i] = f
		}
	}
	return out
}

// Peer represents a paired remote device.
type Peer struct {
	DeviceID        string `json:"device_id"`
	DeviceName      string `json:"device_name"`
	OS              string `json:"os"`
	CertFingerprint string `json:"cert_fingerprint"`
	LastAddr        string `json:"last_addr,omitempty"`
	ManualAddr      string `json:"manual_addr,omitempty"`
	PairedAt        int64  `json:"paired_at"`
	LastSeenAt      int64  `json:"last_seen_at"`
	LastSyncAt      int64  `json:"last_sync_at"`
	Online          bool   `json:"online"`
}

// AppConfig holds persistent user settings and device identity.
type AppConfig struct {
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name"`
	ListenPort           int      `json:"listen_port"`
	Hotkey               string   `json:"hotkey"`
	MaxHistory           int      `json:"max_history"`
	MaxImageBytes        int64    `json:"max_image_bytes"`
	MaxFileDownloadBytes int64    `json:"max_file_download_bytes"`
	UniversalClipboard   bool     `json:"universal_clipboard"`
	AutoPaste            bool     `json:"auto_paste"`
	Paused               bool     `json:"paused"`
	ExcludedApps         []string `json:"excluded_apps"`
}

// DefaultConfig returns sensible defaults for the current OS.
func DefaultConfig(deviceID, deviceName string) AppConfig {
	hotkey := "Alt+V"
	excluded := []string{
		"1Password.exe",
		"Bitwarden.exe",
		"KeePassXC.exe",
		"KeePass.exe",
	}
	if runtime.GOOS == "darwin" {
		hotkey = "Option+V"
		excluded = []string{
			"com.1password.1password",
			"com.agilebits.onepassword7",
			"com.bitwarden.desktop",
			"org.keepassxc.keepassxc",
			"com.apple.keychainaccess",
		}
	}
	return AppConfig{
		DeviceID:             deviceID,
		DeviceName:           deviceName,
		ListenPort:           47831,
		Hotkey:               hotkey,
		MaxHistory:           200,
		MaxImageBytes:        20 * 1024 * 1024,
		MaxFileDownloadBytes: 2 * 1024 * 1024 * 1024,
		UniversalClipboard:   true,
		AutoPaste:            true,
		Paused:               false,
		ExcludedApps:         excluded,
	}
}

type HistoryFilter struct {
	Search         string   `json:"search"`
	Kind           ItemKind `json:"kind"`
	OriginDeviceID string   `json:"origin_device_id"`
	OriginOS       string   `json:"origin_os"`
	Limit          int      `json:"limit"`
	Offset         int      `json:"offset"`
}

type DeviceInfo struct {
	DeviceID        string `json:"device_id"`
	DeviceName      string `json:"device_name"`
	OS              string `json:"os"`
	CertFingerprint string `json:"cert_fingerprint"`
	Port            int    `json:"port"`
}

type DiscoveredDevice struct {
	DeviceID        string `json:"device_id"`
	DeviceName      string `json:"device_name"`
	OS              string `json:"os"`
	CertFingerprint string `json:"cert_fingerprint"`
	Addr            string `json:"addr"`
	Port            int    `json:"port"`
	AlreadyPaired   bool   `json:"already_paired"`
}

type DeltaRequest struct {
	SinceMs int64 `json:"since_ms"`
}

type DeltaResponse struct {
	ServerTimeMs int64              `json:"server_time_ms"`
	Items        []ClipboardItem    `json:"items"`
	Folders      []TemplateFolder   `json:"folders"`
	Templates    []TemplateItem     `json:"templates"`
	Thumbs       map[string][]byte  `json:"thumbs,omitempty"`
}

type SyncMessage struct {
	Type       string          `json:"type"`
	SenderID   string          `json:"sender_id"`
	SentAt     int64           `json:"sent_at"`
	DeviceInfo *DeviceInfo     `json:"device_info,omitempty"`
	Item       *ClipboardItem  `json:"item,omitempty"`
	ThumbPNG   []byte          `json:"thumb_png,omitempty"`
	ID         string          `json:"id,omitempty"`
	DeletedAt  int64           `json:"deleted_at,omitempty"`
	Folder     *TemplateFolder `json:"folder,omitempty"`
	Template   *TemplateItem   `json:"template,omitempty"`
	DeltaReq   *DeltaRequest   `json:"delta_req,omitempty"`
	DeltaResp  *DeltaResponse  `json:"delta_resp,omitempty"`
}

type FileProgressEvent struct {
	ItemID     string `json:"item_id"`
	FileIndex  int    `json:"file_index"`
	FileName   string `json:"file_name"`
	BytesDone  int64  `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
}
