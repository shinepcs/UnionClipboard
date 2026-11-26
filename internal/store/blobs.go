package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validHex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)

func IsValidSHA256(s string) bool {
	return validHex64.MatchString(s)
}

func (s *Store) BlobsDir() string {
	return filepath.Join(s.dataDir, "blobs")
}

func (s *Store) ThumbsDir() string {
	return filepath.Join(s.dataDir, "thumbs")
}

func (s *Store) ReceivedFilesDir() string {
	return filepath.Join(s.dataDir, "received_files")
}

func (s *Store) CertsDir() string {
	return filepath.Join(s.dataDir, "certs")
}

// SaveBlob writes a content-addressed PNG blob and returns its relative path under dataDir.
func (s *Store) SaveBlob(sha256Hex string, data []byte) (string, error) {
	if !IsValidSHA256(sha256Hex) {
		return "", fmt.Errorf("invalid sha256: %s", sha256Hex)
	}
	rel := filepath.Join("blobs", sha256Hex+".png")
	abs := filepath.Join(s.dataDir, rel)
	if _, err := os.Stat(abs); err == nil {
		return filepath.ToSlash(rel), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// SaveThumb writes a content-addressed thumbnail PNG and returns its relative path under dataDir.
func (s *Store) SaveThumb(sha256Hex string, data []byte) (string, error) {
	if !IsValidSHA256(sha256Hex) {
		return "", fmt.Errorf("invalid sha256: %s", sha256Hex)
	}
	rel := filepath.Join("thumbs", sha256Hex+"_thumb.png")
	abs := filepath.Join(s.dataDir, rel)
	if _, err := os.Stat(abs); err == nil {
		return filepath.ToSlash(rel), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func (s *Store) ReadBlobByHash(sha256Hex string) ([]byte, error) {
	if !IsValidSHA256(sha256Hex) {
		return nil, fmt.Errorf("invalid sha256")
	}
	return os.ReadFile(filepath.Join(s.BlobsDir(), sha256Hex+".png"))
}

func (s *Store) ReadThumbByHash(sha256Hex string) ([]byte, error) {
	if !IsValidSHA256(sha256Hex) {
		return nil, fmt.Errorf("invalid sha256")
	}
	return os.ReadFile(filepath.Join(s.ThumbsDir(), sha256Hex+"_thumb.png"))
}

func (s *Store) HasBlob(sha256Hex string) bool {
	if !IsValidSHA256(sha256Hex) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.BlobsDir(), sha256Hex+".png"))
	return err == nil
}

func (s *Store) HasThumb(sha256Hex string) bool {
	if !IsValidSHA256(sha256Hex) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.ThumbsDir(), sha256Hex+"_thumb.png"))
	return err == nil
}

// ReceivedItemDir returns the staging directory for downloaded remote files of an item.
func (s *Store) ReceivedItemDir(itemID string) (string, error) {
	clean := filepath.Base(itemID)
	if clean == "." || clean == ".." || clean == "" || strings.ContainsAny(clean, `/\`) {
		return "", fmt.Errorf("invalid item id: %s", itemID)
	}
	dir := filepath.Join(s.ReceivedFilesDir(), clean)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// ReceivedCacheSize returns total bytes used by received_files cache.
func (s *Store) ReceivedCacheSize() (int64, error) {
	root := s.ReceivedFilesDir()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0, nil
	}
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, statErr := d.Info(); statErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// ClearReceivedFileCache removes all cached remote files and clears LocalCachedPath in DB.
func (s *Store) ClearReceivedFileCache() error {
	if err := os.RemoveAll(s.ReceivedFilesDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(s.ReceivedFilesDir(), 0o700); err != nil {
		return err
	}
	return s.ClearLocalCachedPaths()
}

// GCBlobs removes unreferenced blobs, thumbnails, and received_files directories.
func (s *Store) GCBlobs() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keepHashes := make(map[string]struct{})
	keepItemIDs := make(map[string]struct{})

	rows, err := s.db.Query(`SELECT id, kind, content_hash FROM clipboard_items WHERE deleted_at = 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, kind, hash string
		if err := rows.Scan(&id, &kind, &hash); err == nil {
			if kind == "image" && IsValidSHA256(hash) {
				keepHashes[hash] = struct{}{}
			}
			if kind == "files" {
				keepItemIDs[id] = struct{}{}
			}
		}
	}
	rows.Close()

	tRows, err := s.db.Query(`SELECT id, kind, content_hash FROM templates WHERE deleted_at = 0`)
	if err != nil {
		return err
	}
	for tRows.Next() {
		var id, kind, hash string
		if err := tRows.Scan(&id, &kind, &hash); err == nil {
			if kind == "image" && IsValidSHA256(hash) {
				keepHashes[hash] = struct{}{}
			}
			if kind == "files" {
				keepItemIDs[id] = struct{}{}
			}
		}
	}
	tRows.Close()

	if entries, err := os.ReadDir(s.BlobsDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			hash := strings.TrimSuffix(e.Name(), ".png")
			if _, ok := keepHashes[hash]; !ok {
				_ = os.Remove(filepath.Join(s.BlobsDir(), e.Name()))
			}
		}
	}

	if entries, err := os.ReadDir(s.ThumbsDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			hash := strings.TrimSuffix(e.Name(), "_thumb.png")
			if _, ok := keepHashes[hash]; !ok {
				_ = os.Remove(filepath.Join(s.ThumbsDir(), e.Name()))
			}
		}
	}

	if entries, err := os.ReadDir(s.ReceivedFilesDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, ok := keepItemIDs[e.Name()]; !ok {
				_ = os.RemoveAll(filepath.Join(s.ReceivedFilesDir(), e.Name()))
			}
		}
	}
	return nil
}
