package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"unionclip/internal/model"
)

type Store struct {
	dataDir string
	db      *sql.DB
	mu      sync.Mutex
}

// DefaultDataDir returns the OS-standard application data directory.
func DefaultDataDir() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "UnionClipboard"), nil
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "UnionClipboard"), nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "UnionClipboard"), nil
}

func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		var err error
		dataDir, err = DefaultDataDir()
		if err != nil {
			return nil, err
		}
	}
	for _, sub := range []string{"", "blobs", "thumbs", "received_files", "certs"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}

	dbPath := filepath.Join(dataDir, "unionclip.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{dataDir: dataDir, db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) DataDir() string {
	return s.dataDir
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	pragmas := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
	}
	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("pragma %s: %w", p, err)
		}
	}

	schema := `
CREATE TABLE IF NOT EXISTS config (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS clipboard_items (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	text_content TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	blob_path TEXT NOT NULL DEFAULT '',
	thumb_path TEXT NOT NULL DEFAULT '',
	byte_size INTEGER NOT NULL DEFAULT 0,
	image_width INTEGER NOT NULL DEFAULT 0,
	image_height INTEGER NOT NULL DEFAULT 0,
	files_json TEXT NOT NULL DEFAULT '[]',
	origin_device_id TEXT NOT NULL,
	origin_device_name TEXT NOT NULL DEFAULT '',
	origin_os TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	deleted_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_items_updated ON clipboard_items(deleted_at, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_hash ON clipboard_items(content_hash, deleted_at);

CREATE TABLE IF NOT EXISTS template_folders (
	id TEXT PRIMARY KEY,
	parent_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL,
	deleted_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS templates (
	id TEXT PRIMARY KEY,
	folder_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	kind TEXT NOT NULL,
	content_hash TEXT NOT NULL DEFAULT '',
	text_content TEXT NOT NULL DEFAULT '',
	blob_path TEXT NOT NULL DEFAULT '',
	thumb_path TEXT NOT NULL DEFAULT '',
	byte_size INTEGER NOT NULL DEFAULT 0,
	image_width INTEGER NOT NULL DEFAULT 0,
	image_height INTEGER NOT NULL DEFAULT 0,
	files_json TEXT NOT NULL DEFAULT '[]',
	origin_device_id TEXT NOT NULL DEFAULT '',
	sort_order INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL,
	deleted_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS peers (
	device_id TEXT PRIMARY KEY,
	device_name TEXT NOT NULL,
	os TEXT NOT NULL,
	cert_fingerprint TEXT NOT NULL UNIQUE,
	last_addr TEXT NOT NULL DEFAULT '',
	manual_addr TEXT NOT NULL DEFAULT '',
	paired_at INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL DEFAULT 0,
	last_sync_at INTEGER NOT NULL DEFAULT 0
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

func (s *Store) LoadOrInitConfig() (model.AppConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var raw string
	err := s.db.QueryRow(`SELECT value FROM config WHERE key = 'app_config'`).Scan(&raw)
	if err == nil && raw != "" {
		var cfg model.AppConfig
		if uErr := json.Unmarshal([]byte(raw), &cfg); uErr == nil && cfg.DeviceID != "" {
			if cfg.MaxHistory <= 0 {
				cfg.MaxHistory = 200
			}
			if cfg.MaxImageBytes <= 0 {
				cfg.MaxImageBytes = 20 * 1024 * 1024
			}
			if cfg.MaxFileDownloadBytes <= 0 {
				cfg.MaxFileDownloadBytes = 2 * 1024 * 1024 * 1024
			}
			if cfg.ListenPort <= 0 {
				cfg.ListenPort = 47831
			}
			return cfg, nil
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.AppConfig{}, err
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = runtime.GOOS + "-device"
	}
	cfg := model.DefaultConfig(uuid.NewString(), hostname)
	if err := s.saveConfigLocked(cfg); err != nil {
		return model.AppConfig{}, err
	}
	return cfg, nil
}

func (s *Store) SaveConfig(cfg model.AppConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveConfigLocked(cfg)
}

func (s *Store) saveConfigLocked(cfg model.AppConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO config(key, value) VALUES('app_config', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		string(b),
	)
	return err
}

// UpsertLocalItem inserts a new local clipboard capture or bumps an existing item with identical content_hash.
func (s *Store) UpsertLocalItem(item model.ClipboardItem) (model.ClipboardItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	now := item.UpdatedAt
	if now == 0 {
		now = time.Now().UnixMilli()
		item.UpdatedAt = now
	}
	if item.CreatedAt == 0 {
		item.CreatedAt = now
	}

	var existingID string
	var createdAt int64
	err := s.db.QueryRow(
		`SELECT id, created_at FROM clipboard_items WHERE content_hash = ? AND deleted_at = 0 ORDER BY updated_at DESC LIMIT 1`,
		item.ContentHash,
	).Scan(&existingID, &createdAt)
	if err == nil && existingID != "" {
		item.ID = existingID
		item.CreatedAt = createdAt
	}

	filesJSON, err := json.Marshal(item.Files)
	if err != nil {
		return item, err
	}
	_, err = s.db.Exec(`
INSERT INTO clipboard_items(
	id, kind, content_hash, text_content, title, blob_path, thumb_path, byte_size,
	image_width, image_height, files_json, origin_device_id, origin_device_name, origin_os,
	created_at, updated_at, deleted_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	kind=excluded.kind,
	content_hash=excluded.content_hash,
	text_content=excluded.text_content,
	title=excluded.title,
	blob_path=CASE WHEN excluded.blob_path != '' THEN excluded.blob_path ELSE clipboard_items.blob_path END,
	thumb_path=CASE WHEN excluded.thumb_path != '' THEN excluded.thumb_path ELSE clipboard_items.thumb_path END,
	byte_size=excluded.byte_size,
	image_width=excluded.image_width,
	image_height=excluded.image_height,
	files_json=excluded.files_json,
	origin_device_id=excluded.origin_device_id,
	origin_device_name=excluded.origin_device_name,
	origin_os=excluded.origin_os,
	updated_at=excluded.updated_at,
	deleted_at=excluded.deleted_at
`,
		item.ID, string(item.Kind), item.ContentHash, item.TextContent, item.Title, item.BlobPath, item.ThumbPath,
		item.ByteSize, item.ImageWidth, item.ImageHeight, string(filesJSON),
		item.OriginDeviceID, item.OriginDeviceName, item.OriginOS,
		item.CreatedAt, item.UpdatedAt, item.DeletedAt,
	)
	return item, err
}

// UpsertRemoteItem applies a synced item using LWW on updated_at / deleted_at.
// Returns true if the local DB state changed.
func (s *Store) UpsertRemoteItem(item model.ClipboardItem) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var existingUpdated, existingDeleted int64
	var existingBlob, existingThumb, existingFilesJSON string
	err := s.db.QueryRow(
		`SELECT updated_at, deleted_at, blob_path, thumb_path, files_json FROM clipboard_items WHERE id = ?`,
		item.ID,
	).Scan(&existingUpdated, &existingDeleted, &existingBlob, &existingThumb, &existingFilesJSON)
	if err == nil {
		existingMax := existingUpdated
		if existingDeleted > existingMax {
			existingMax = existingDeleted
		}
		incomingMax := item.UpdatedAt
		if item.DeletedAt > incomingMax {
			incomingMax = item.DeletedAt
		}
		if incomingMax <= existingMax {
			return false, nil
		}
		// Preserve locally downloaded blob/thumb/cached files if content hash didn't change
		if item.BlobPath == "" {
			item.BlobPath = existingBlob
		}
		if item.ThumbPath == "" {
			item.ThumbPath = existingThumb
		}
		if len(item.Files) > 0 && existingFilesJSON != "" {
			var prevFiles []model.FileEntry
			if json.Unmarshal([]byte(existingFilesJSON), &prevFiles) == nil && len(prevFiles) == len(item.Files) {
				for i := range item.Files {
					if item.Files[i].LocalCachedPath == "" && item.Files[i].RelativeName == prevFiles[i].RelativeName {
						item.Files[i].LocalCachedPath = prevFiles[i].LocalCachedPath
					}
				}
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	filesJSON, err := json.Marshal(item.Files)
	if err != nil {
		return false, err
	}
	_, err = s.db.Exec(`
INSERT INTO clipboard_items(
	id, kind, content_hash, text_content, title, blob_path, thumb_path, byte_size,
	image_width, image_height, files_json, origin_device_id, origin_device_name, origin_os,
	created_at, updated_at, deleted_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	kind=excluded.kind,
	content_hash=excluded.content_hash,
	text_content=excluded.text_content,
	title=excluded.title,
	blob_path=excluded.blob_path,
	thumb_path=excluded.thumb_path,
	byte_size=excluded.byte_size,
	image_width=excluded.image_width,
	image_height=excluded.image_height,
	files_json=excluded.files_json,
	origin_device_id=excluded.origin_device_id,
	origin_device_name=excluded.origin_device_name,
	origin_os=excluded.origin_os,
	updated_at=excluded.updated_at,
	deleted_at=excluded.deleted_at
`,
		item.ID, string(item.Kind), item.ContentHash, item.TextContent, item.Title, item.BlobPath, item.ThumbPath,
		item.ByteSize, item.ImageWidth, item.ImageHeight, string(filesJSON),
		item.OriginDeviceID, item.OriginDeviceName, item.OriginOS,
		item.CreatedAt, item.UpdatedAt, item.DeletedAt,
	)
	return err == nil, err
}

func (s *Store) GetItem(id string) (model.ClipboardItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getItemLocked(id)
}

func (s *Store) getItemLocked(id string) (model.ClipboardItem, error) {
	row := s.db.QueryRow(`
SELECT id, kind, content_hash, text_content, title, blob_path, thumb_path, byte_size,
       image_width, image_height, files_json, origin_device_id, origin_device_name, origin_os,
       created_at, updated_at, deleted_at
FROM clipboard_items WHERE id = ?`, id)
	return scanItem(row)
}

func (s *Store) UpdateItemText(id, newText, newTitle, newHash string, updatedAt int64) (model.ClipboardItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE clipboard_items SET text_content=?, title=?, content_hash=?, byte_size=?, updated_at=? WHERE id=? AND deleted_at=0`,
		newText, newTitle, newHash, int64(len(newText)), updatedAt, id,
	)
	if err != nil {
		return model.ClipboardItem{}, err
	}
	return s.getItemLocked(id)
}

func (s *Store) UpdateItemMediaPaths(id, blobPath, thumbPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE clipboard_items SET blob_path=CASE WHEN ? != '' THEN ? ELSE blob_path END, thumb_path=CASE WHEN ? != '' THEN ? ELSE thumb_path END WHERE id=?`,
		blobPath, blobPath, thumbPath, thumbPath, id,
	)
	return err
}

func (s *Store) UpdateItemFiles(id string, files []model.FileEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(files)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE clipboard_items SET files_json = ? WHERE id = ?`, string(b), id)
	return err
}

func (s *Store) ClearLocalCachedPaths() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT id, files_json FROM clipboard_items WHERE kind = 'files'`)
	if err != nil {
		return err
	}
	type patch struct {
		id   string
		json string
	}
	var patches []patch
	for rows.Next() {
		var id, fj string
		if rows.Scan(&id, &fj) == nil {
			var files []model.FileEntry
			if json.Unmarshal([]byte(fj), &files) == nil {
				changed := false
				for i := range files {
					if files[i].LocalCachedPath != "" {
						files[i].LocalCachedPath = ""
						changed = true
					}
				}
				if changed {
					if b, err := json.Marshal(files); err == nil {
						patches = append(patches, patch{id: id, json: string(b)})
					}
				}
			}
		}
	}
	rows.Close()
	for _, p := range patches {
		if _, err := s.db.Exec(`UPDATE clipboard_items SET files_json = ? WHERE id = ?`, p.json, p.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SoftDeleteItem(id string, deletedAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE clipboard_items SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at = 0`, deletedAt, deletedAt, id)
	return err
}

func (s *Store) ClearHistory(deletedAt int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id FROM clipboard_items WHERE deleted_at = 0`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	_, err = s.db.Exec(`UPDATE clipboard_items SET deleted_at = ?, updated_at = ? WHERE deleted_at = 0`, deletedAt, deletedAt)
	return ids, err
}

func (s *Store) ListHistory(f model.HistoryFilter) ([]model.ClipboardItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var where []string
	var args []any
	where = append(where, "deleted_at = 0")

	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}
	if f.OriginDeviceID != "" {
		where = append(where, "origin_device_id = ?")
		args = append(args, f.OriginDeviceID)
	}
	if f.OriginOS != "" {
		where = append(where, "origin_os = ?")
		args = append(args, f.OriginOS)
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		like := "%" + q + "%"
		where = append(where, "(title LIKE ? OR text_content LIKE ? OR files_json LIKE ?)")
		args = append(args, like, like, like)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	query := fmt.Sprintf(`
SELECT id, kind, content_hash, text_content, title, blob_path, thumb_path, byte_size,
       image_width, image_height, files_json, origin_device_id, origin_device_name, origin_os,
       created_at, updated_at, deleted_at
FROM clipboard_items
WHERE %s
ORDER BY updated_at DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))
	args = append(args, limit, f.Offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ClipboardItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PruneHistory deletes oldest items beyond maxCount and purges old tombstones older than 30 days.
func (s *Store) PruneHistory(maxCount int) error {
	if maxCount <= 0 {
		return nil
	}
	s.mu.Lock()
	_, err := s.db.Exec(`
DELETE FROM clipboard_items
WHERE id IN (
	SELECT id FROM clipboard_items
	WHERE deleted_at = 0
	ORDER BY updated_at DESC
	LIMIT -1 OFFSET ?
)`, maxCount)
	if err == nil {
		cutoff := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
		_, _ = s.db.Exec(`DELETE FROM clipboard_items WHERE deleted_at > 0 AND deleted_at < ?`, cutoff)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.GCBlobs()
}

// Template Folders

func (s *Store) UpsertTemplateFolder(f model.TemplateFolder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.UpdatedAt == 0 {
		f.UpdatedAt = time.Now().UnixMilli()
	}
	var existingUpdated, existingDeleted int64
	err := s.db.QueryRow(`SELECT updated_at, deleted_at FROM template_folders WHERE id = ?`, f.ID).Scan(&existingUpdated, &existingDeleted)
	if err == nil {
		existingMax := maxInt64(existingUpdated, existingDeleted)
		incomingMax := maxInt64(f.UpdatedAt, f.DeletedAt)
		if incomingMax < existingMax {
			return nil
		}
	}
	_, err = s.db.Exec(`
INSERT INTO template_folders(id, parent_id, name, sort_order, updated_at, deleted_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	parent_id=excluded.parent_id,
	name=excluded.name,
	sort_order=excluded.sort_order,
	updated_at=excluded.updated_at,
	deleted_at=excluded.deleted_at
`, f.ID, f.ParentID, f.Name, f.SortOrder, f.UpdatedAt, f.DeletedAt)
	return err
}

func (s *Store) ListTemplateFolders() ([]model.TemplateFolder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, parent_id, name, sort_order, updated_at, deleted_at FROM template_folders WHERE deleted_at = 0 ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TemplateFolder
	for rows.Next() {
		var f model.TemplateFolder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.SortOrder, &f.UpdatedAt, &f.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) SoftDeleteTemplateFolder(id string, deletedAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE template_folders SET deleted_at=?, updated_at=? WHERE id=? AND deleted_at=0`, deletedAt, deletedAt, id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE templates SET folder_id='', updated_at=? WHERE folder_id=? AND deleted_at=0`, deletedAt, id)
	return err
}

// Templates

func (s *Store) UpsertTemplate(t model.TemplateItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.UpdatedAt == 0 {
		t.UpdatedAt = time.Now().UnixMilli()
	}
	var existingUpdated, existingDeleted int64
	var existingBlob, existingThumb string
	err := s.db.QueryRow(`SELECT updated_at, deleted_at, blob_path, thumb_path FROM templates WHERE id = ?`, t.ID).Scan(&existingUpdated, &existingDeleted, &existingBlob, &existingThumb)
	if err == nil {
		if maxInt64(t.UpdatedAt, t.DeletedAt) < maxInt64(existingUpdated, existingDeleted) {
			return nil
		}
		if t.BlobPath == "" {
			t.BlobPath = existingBlob
		}
		if t.ThumbPath == "" {
			t.ThumbPath = existingThumb
		}
	}
	fj, err := json.Marshal(t.Files)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO templates(
	id, folder_id, title, kind, content_hash, text_content, blob_path, thumb_path,
	byte_size, image_width, image_height, files_json, origin_device_id, sort_order, updated_at, deleted_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	folder_id=excluded.folder_id,
	title=excluded.title,
	kind=excluded.kind,
	content_hash=excluded.content_hash,
	text_content=excluded.text_content,
	blob_path=excluded.blob_path,
	thumb_path=excluded.thumb_path,
	byte_size=excluded.byte_size,
	image_width=excluded.image_width,
	image_height=excluded.image_height,
	files_json=excluded.files_json,
	origin_device_id=excluded.origin_device_id,
	sort_order=excluded.sort_order,
	updated_at=excluded.updated_at,
	deleted_at=excluded.deleted_at
`, t.ID, t.FolderID, t.Title, string(t.Kind), t.ContentHash, t.TextContent, t.BlobPath, t.ThumbPath,
		t.ByteSize, t.ImageWidth, t.ImageHeight, string(fj), t.OriginDeviceID, t.SortOrder, t.UpdatedAt, t.DeletedAt)
	return err
}

func (s *Store) GetTemplate(id string) (model.TemplateItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`
SELECT id, folder_id, title, kind, content_hash, text_content, blob_path, thumb_path,
       byte_size, image_width, image_height, files_json, origin_device_id, sort_order, updated_at, deleted_at
FROM templates WHERE id = ?`, id)
	return scanTemplate(row)
}

func (s *Store) ListTemplates() ([]model.TemplateItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`
SELECT id, folder_id, title, kind, content_hash, text_content, blob_path, thumb_path,
       byte_size, image_width, image_height, files_json, origin_device_id, sort_order, updated_at, deleted_at
FROM templates WHERE deleted_at = 0 ORDER BY sort_order ASC, title ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TemplateItem
	for rows.Next() {
		it, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) SoftDeleteTemplate(id string, deletedAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE templates SET deleted_at=?, updated_at=? WHERE id=? AND deleted_at=0`, deletedAt, deletedAt, id)
	return err
}

func (s *Store) UpdateTemplateMediaPaths(id, blobPath, thumbPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE templates SET blob_path=CASE WHEN ? != '' THEN ? ELSE blob_path END, thumb_path=CASE WHEN ? != '' THEN ? ELSE thumb_path END WHERE id=?`,
		blobPath, blobPath, thumbPath, thumbPath, id,
	)
	return err
}

// Delta Sync queries

func (s *Store) ListChangesSince(sinceMs int64) (model.DeltaResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := model.DeltaResponse{
		ServerTimeMs: time.Now().UnixMilli(),
		Thumbs:       make(map[string][]byte),
	}

	iRows, err := s.db.Query(`
SELECT id, kind, content_hash, text_content, title, blob_path, thumb_path, byte_size,
       image_width, image_height, files_json, origin_device_id, origin_device_name, origin_os,
       created_at, updated_at, deleted_at
FROM clipboard_items
WHERE updated_at > ? OR deleted_at > ?
ORDER BY updated_at ASC`, sinceMs, sinceMs)
	if err != nil {
		return resp, err
	}
	for iRows.Next() {
		it, err := scanItem(iRows)
		if err != nil {
			iRows.Close()
			return resp, err
		}
		resp.Items = append(resp.Items, it)
	}
	iRows.Close()

	fRows, err := s.db.Query(`
SELECT id, parent_id, name, sort_order, updated_at, deleted_at
FROM template_folders
WHERE updated_at > ? OR deleted_at > ?
ORDER BY updated_at ASC`, sinceMs, sinceMs)
	if err != nil {
		return resp, err
	}
	for fRows.Next() {
		var f model.TemplateFolder
		if err := fRows.Scan(&f.ID, &f.ParentID, &f.Name, &f.SortOrder, &f.UpdatedAt, &f.DeletedAt); err != nil {
			fRows.Close()
			return resp, err
		}
		resp.Folders = append(resp.Folders, f)
	}
	fRows.Close()

	tRows, err := s.db.Query(`
SELECT id, folder_id, title, kind, content_hash, text_content, blob_path, thumb_path,
       byte_size, image_width, image_height, files_json, origin_device_id, sort_order, updated_at, deleted_at
FROM templates
WHERE updated_at > ? OR deleted_at > ?
ORDER BY updated_at ASC`, sinceMs, sinceMs)
	if err != nil {
		return resp, err
	}
	for tRows.Next() {
		it, err := scanTemplate(tRows)
		if err != nil {
			tRows.Close()
			return resp, err
		}
		resp.Templates = append(resp.Templates, it)
	}
	tRows.Close()

	return resp, nil
}

// Peers

func (s *Store) UpsertPeer(p model.Peer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
INSERT INTO peers(device_id, device_name, os, cert_fingerprint, last_addr, manual_addr, paired_at, last_seen_at, last_sync_at)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(device_id) DO UPDATE SET
	device_name=excluded.device_name,
	os=excluded.os,
	cert_fingerprint=excluded.cert_fingerprint,
	last_addr=CASE WHEN excluded.last_addr != '' THEN excluded.last_addr ELSE peers.last_addr END,
	manual_addr=CASE WHEN excluded.manual_addr != '' THEN excluded.manual_addr ELSE peers.manual_addr END,
	last_seen_at=MAX(peers.last_seen_at, excluded.last_seen_at),
	last_sync_at=MAX(peers.last_sync_at, excluded.last_sync_at)
`, p.DeviceID, p.DeviceName, p.OS, p.CertFingerprint, p.LastAddr, p.ManualAddr, p.PairedAt, p.LastSeenAt, p.LastSyncAt)
	return err
}

func (s *Store) ListPeers() ([]model.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT device_id, device_name, os, cert_fingerprint, last_addr, manual_addr, paired_at, last_seen_at, last_sync_at FROM peers ORDER BY device_name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Peer
	for rows.Next() {
		var p model.Peer
		if err := rows.Scan(&p.DeviceID, &p.DeviceName, &p.OS, &p.CertFingerprint, &p.LastAddr, &p.ManualAddr, &p.PairedAt, &p.LastSeenAt, &p.LastSyncAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPeer(deviceID string) (model.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p model.Peer
	err := s.db.QueryRow(`SELECT device_id, device_name, os, cert_fingerprint, last_addr, manual_addr, paired_at, last_seen_at, last_sync_at FROM peers WHERE device_id = ?`, deviceID).
		Scan(&p.DeviceID, &p.DeviceName, &p.OS, &p.CertFingerprint, &p.LastAddr, &p.ManualAddr, &p.PairedAt, &p.LastSeenAt, &p.LastSyncAt)
	return p, err
}

func (s *Store) GetPeerByFingerprint(fp string) (model.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p model.Peer
	err := s.db.QueryRow(`SELECT device_id, device_name, os, cert_fingerprint, last_addr, manual_addr, paired_at, last_seen_at, last_sync_at FROM peers WHERE cert_fingerprint = ?`, fp).
		Scan(&p.DeviceID, &p.DeviceName, &p.OS, &p.CertFingerprint, &p.LastAddr, &p.ManualAddr, &p.PairedAt, &p.LastSeenAt, &p.LastSyncAt)
	return p, err
}

func (s *Store) UpdatePeerSeen(deviceID, lastAddr string, seenAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE peers SET last_addr = CASE WHEN ? != '' THEN ? ELSE last_addr END, last_seen_at = MAX(last_seen_at, ?) WHERE device_id = ?`,
		lastAddr, lastAddr, seenAt, deviceID,
	)
	return err
}

func (s *Store) UpdatePeerLastSync(deviceID string, syncAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE peers SET last_sync_at = MAX(last_sync_at, ?) WHERE device_id = ?`, syncAt, deviceID)
	return err
}

func (s *Store) UpdatePeerManualAddr(deviceID, manualAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE peers SET manual_addr = ? WHERE device_id = ?`, manualAddr, deviceID)
	return err
}

func (s *Store) DeletePeer(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM peers WHERE device_id = ?`, deviceID)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanItem(sc scanner) (model.ClipboardItem, error) {
	var it model.ClipboardItem
	var kindStr, filesJSON string
	err := sc.Scan(
		&it.ID, &kindStr, &it.ContentHash, &it.TextContent, &it.Title, &it.BlobPath, &it.ThumbPath,
		&it.ByteSize, &it.ImageWidth, &it.ImageHeight, &filesJSON,
		&it.OriginDeviceID, &it.OriginDeviceName, &it.OriginOS,
		&it.CreatedAt, &it.UpdatedAt, &it.DeletedAt,
	)
	if err != nil {
		return it, err
	}
	it.Kind = model.ItemKind(kindStr)
	if filesJSON != "" && filesJSON != "[]" {
		_ = json.Unmarshal([]byte(filesJSON), &it.Files)
	}
	return it, nil
}

func scanTemplate(sc scanner) (model.TemplateItem, error) {
	var t model.TemplateItem
	var kindStr, filesJSON string
	err := sc.Scan(
		&t.ID, &t.FolderID, &t.Title, &kindStr, &t.ContentHash, &t.TextContent, &t.BlobPath, &t.ThumbPath,
		&t.ByteSize, &t.ImageWidth, &t.ImageHeight, &filesJSON,
		&t.OriginDeviceID, &t.SortOrder, &t.UpdatedAt, &t.DeletedAt,
	)
	if err != nil {
		return t, err
	}
	t.Kind = model.ItemKind(kindStr)
	if filesJSON != "" && filesJSON != "[]" {
		_ = json.Unmarshal([]byte(filesJSON), &t.Files)
	}
	return t, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
