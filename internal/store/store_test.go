package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"unionclip/internal/model"
)

func TestStoreHistoryDedupeAndPrune(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	cfg, err := s.LoadOrInitConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DeviceID == "" {
		t.Fatal("expected generated device ID")
	}

	it1, err := s.UpsertLocalItem(model.ClipboardItem{
		Kind:           model.KindText,
		ContentHash:    "hash-a",
		TextContent:    "hello",
		Title:          "hello",
		OriginDeviceID: cfg.DeviceID,
		CreatedAt:      1000,
		UpdatedAt:      1000,
	})
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}

	_, err = s.UpsertLocalItem(model.ClipboardItem{
		Kind:           model.KindText,
		ContentHash:    "hash-b",
		TextContent:    "world",
		Title:          "world",
		OriginDeviceID: cfg.DeviceID,
		CreatedAt:      2000,
		UpdatedAt:      2000,
	})
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	// Re-copy hash-a -> should reuse ID and move to top
	it1Again, err := s.UpsertLocalItem(model.ClipboardItem{
		Kind:           model.KindText,
		ContentHash:    "hash-a",
		TextContent:    "hello",
		Title:          "hello",
		OriginDeviceID: cfg.DeviceID,
		UpdatedAt:      3000,
	})
	if err != nil {
		t.Fatalf("upsert 1 again: %v", err)
	}
	if it1Again.ID != it1.ID {
		t.Fatalf("expected deduplicated ID %s, got %s", it1.ID, it1Again.ID)
	}

	list, err := s.ListHistory(model.HistoryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != it1.ID {
		t.Fatalf("expected [hash-a, hash-b], got %+v", list)
	}

	// Save blob for item that will be pruned
	sum := sha256.Sum256([]byte("imgdata"))
	hexHash := hex.EncodeToString(sum[:])
	blobRel, err := s.SaveBlob(hexHash, []byte("imgdata"))
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	_, err = s.UpsertLocalItem(model.ClipboardItem{
		Kind:           model.KindImage,
		ContentHash:    hexHash,
		BlobPath:       blobRel,
		Title:          "img",
		OriginDeviceID: cfg.DeviceID,
		CreatedAt:      500,
		UpdatedAt:      500,
	})
	if err != nil {
		t.Fatalf("upsert image: %v", err)
	}

	// Prune to 2 items -> image at ts=500 gets deleted & GC'd
	if err := s.PruneHistory(2); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if s.HasBlob(hexHash) {
		t.Fatal("expected unreferenced blob to be GC'd after prune")
	}

	// Search filter
	filtered, err := s.ListHistory(model.HistoryFilter{Search: "wor"})
	if err != nil || len(filtered) != 1 || filtered[0].TextContent != "world" {
		t.Fatalf("unexpected search result: %+v, err=%v", filtered, err)
	}
}

func TestStoreTemplatesAndDeltaAndPeers(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	f := model.TemplateFolder{ID: "f1", Name: "Work", SortOrder: 1, UpdatedAt: 100}
	if err := s.UpsertTemplateFolder(f); err != nil {
		t.Fatalf("upsert folder: %v", err)
	}
	tp := model.TemplateItem{
		ID:             "t1",
		FolderID:       "f1",
		Title:          "Greeting",
		Kind:           model.KindText,
		ContentHash:    "h1",
		TextContent:    "안녕하세요",
		OriginDeviceID: "dev1",
		UpdatedAt:      200,
	}
	if err := s.UpsertTemplate(tp); err != nil {
		t.Fatalf("upsert template: %v", err)
	}

	delta, err := s.ListChangesSince(150)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if len(delta.Templates) != 1 || len(delta.Folders) != 0 {
		t.Fatalf("unexpected delta: %+v", delta)
	}

	if err := s.SoftDeleteTemplate("t1", 300); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	list, err := s.ListTemplates()
	if err != nil || len(list) != 0 {
		t.Fatalf("expected 0 active templates, got %d (%v)", len(list), err)
	}

	// Peer CRUD
	p := model.Peer{
		DeviceID:        "peer1",
		DeviceName:      "WindowsPC",
		OS:              "windows",
		CertFingerprint: "abcd1234",
		PairedAt:        100,
	}
	if err := s.UpsertPeer(p); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	got, err := s.GetPeerByFingerprint("abcd1234")
	if err != nil || got.DeviceID != "peer1" {
		t.Fatalf("get peer by fp: %+v, err=%v", got, err)
	}

	// Received cache check
	recDir, err := s.ReceivedItemDir("item1")
	if err != nil {
		t.Fatalf("received item dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(recDir, "a.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	sz, err := s.ReceivedCacheSize()
	if err != nil || sz != 5 {
		t.Fatalf("expected cache size 5, got %d (%v)", sz, err)
	}
	if err := s.ClearReceivedFileCache(); err != nil {
		t.Fatalf("clear cache: %v", err)
	}
	sz2, _ := s.ReceivedCacheSize()
	if sz2 != 0 {
		t.Fatalf("expected cache size 0 after clear, got %d", sz2)
	}
}
