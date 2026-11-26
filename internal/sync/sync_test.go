package sync

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"unionclip/internal/clipboard"
	"unionclip/internal/model"
	"unionclip/internal/security"
	"unionclip/internal/store"
)

type testNode struct {
	st      *store.Store
	mgr     *Manager
	addr    string
	mu      sync.Mutex
	gotItem []model.ClipboardItem
	blobs   chan string
}

func newTestNode(t *testing.T, id, name, osName string) *testNode {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig(id, name)
	_ = st.SaveConfig(cfg)
	cert, fp, err := security.LoadOrCreateDeviceCert(st.CertsDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	tn := &testNode{
		st:    st,
		blobs: make(chan string, 8),
	}
	info := model.DeviceInfo{
		DeviceID:        id,
		DeviceName:      name,
		OS:              osName,
		CertFingerprint: fp,
	}
	mgr := NewManager(st, cert, fp, info, func() model.AppConfig { return cfg }, Callbacks{
		OnRemoteItem: func(it model.ClipboardItem) {
			tn.mu.Lock()
			tn.gotItem = append(tn.gotItem, it)
			tn.mu.Unlock()
		},
		OnImageBlobReady: func(itemID string) {
			select {
			case tn.blobs <- itemID:
			default:
			}
		},
	})
	mgr.SetBindHost("127.0.0.1")
	port, err := mgr.Start(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	tn.mgr = mgr
	tn.addr = fmt.Sprintf("127.0.0.1:%d", port)
	t.Cleanup(func() {
		mgr.Stop()
		_ = st.Close()
	})
	return tn
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func TestTwoNodePairingSyncAndOnDemandFiles(t *testing.T) {
	nodeA := newTestNode(t, "dev-mac", "MacBook", "darwin")
	nodeB := newTestNode(t, "dev-win", "WindowsPC", "windows")

	// Unpaired mTLS call must be rejected with 403
	unpairedClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: security.PairingClientTLSConfig(nodeB.mgr.cert)},
	}
	res, err := unpairedClient.Get("https://" + nodeA.addr + "/api/v1/blobs/0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for unpaired client, got %d", res.StatusCode)
	}

	// Start pairing on A, wrong PIN rejected, valid PIN succeeds
	pin, _, err := nodeA.mgr.Pairing().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	badPin := "000000"
	if pin == badPin {
		badPin = "111111"
	}
	if _, err := nodeB.mgr.PairWithRemote(context.Background(), nodeA.addr, badPin); err == nil {
		t.Fatal("expected wrong PIN to fail")
	}

	peerA, err := nodeB.mgr.PairWithRemote(context.Background(), nodeA.addr, pin)
	if err != nil {
		t.Fatalf("PairWithRemote failed: %v", err)
	}
	if peerA.DeviceID != "dev-mac" {
		t.Fatalf("unexpected peer: %+v", peerA)
	}

	waitUntil(t, 5*time.Second, func() bool {
		return nodeB.mgr.IsPeerOnline("dev-mac") && nodeA.mgr.IsPeerOnline("dev-win")
	}, "peers to become mutually online")

	// 1. Text sync A -> B
	txtItem, err := nodeA.st.UpsertLocalItem(model.ClipboardItem{
		Kind:             model.KindText,
		ContentHash:      clipboard.HashText("안녕하세요 Mac->Win"),
		TextContent:      "안녕하세요 Mac->Win",
		Title:            "안녕하세요 Mac->Win",
		OriginDeviceID:   "dev-mac",
		OriginDeviceName: "MacBook",
		OriginOS:         "darwin",
		CreatedAt:        1000,
		UpdatedAt:        1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeA.mgr.BroadcastItemUpsert(txtItem, nil)

	waitUntil(t, 5*time.Second, func() bool {
		it, err := nodeB.st.GetItem(txtItem.ID)
		return err == nil && it.TextContent == "안녕하세요 Mac->Win"
	}, "text item synced to nodeB")

	// 2. Image sync A -> B (metadata + thumb + async full PNG blob)
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	img.SetNRGBA(5, 5, color.NRGBA{R: 255, G: 10, B: 20, A: 255})
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	pngBytes := pngBuf.Bytes()
	imgHash := clipboard.HashBytes(pngBytes)
	thumbBytes, _ := clipboard.MakeThumbnailPNG(pngBytes, 16, 16)
	blobRel, _ := nodeA.st.SaveBlob(imgHash, pngBytes)
	thumbRel, _ := nodeA.st.SaveThumb(imgHash, thumbBytes)

	imgItem, err := nodeA.st.UpsertLocalItem(model.ClipboardItem{
		Kind:             model.KindImage,
		ContentHash:      imgHash,
		Title:            "이미지 (20x20)",
		BlobPath:         blobRel,
		ThumbPath:        thumbRel,
		ByteSize:         int64(len(pngBytes)),
		ImageWidth:       20,
		ImageHeight:      20,
		OriginDeviceID:   "dev-mac",
		OriginDeviceName: "MacBook",
		OriginOS:         "darwin",
		CreatedAt:        2000,
		UpdatedAt:        2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeA.mgr.BroadcastItemUpsert(imgItem, thumbBytes)

	waitUntil(t, 5*time.Second, func() bool {
		return nodeB.st.HasBlob(imgHash) && nodeB.st.HasThumb(imgHash)
	}, "image blob & thumb synced to nodeB")

	// 3. File + Folder metadata sync ONLY, then on-demand download
	srcRoot := t.TempDir()
	srcFile := filepath.Join(srcRoot, "hello.txt")
	if err := os.WriteFile(srcFile, []byte("file-payload-123"), 0o600); err != nil {
		t.Fatal(err)
	}
	srcSubDir := filepath.Join(srcRoot, "MyFolder")
	if err := os.MkdirAll(filepath.Join(srcSubDir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSubDir, "nested", "inner.txt"), []byte("inside-folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	files := []model.FileEntry{
		{RelativeName: "hello.txt", OrigPath: srcFile, Size: 16, IsDir: false, ModTime: 123},
		{RelativeName: "MyFolder", OrigPath: srcSubDir, Size: 0, IsDir: true, ModTime: 123},
	}
	fileItem, err := nodeA.st.UpsertLocalItem(model.ClipboardItem{
		Kind:             model.KindFiles,
		ContentHash:      clipboard.HashFiles(files),
		Title:            "hello.txt 외 1개",
		Files:            files,
		OriginDeviceID:   "dev-mac",
		OriginDeviceName: "MacBook",
		OriginOS:         "darwin",
		CreatedAt:        3000,
		UpdatedAt:        3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeA.mgr.BroadcastItemUpsert(fileItem, nil)

	waitUntil(t, 5*time.Second, func() bool {
		it, err := nodeB.st.GetItem(fileItem.ID)
		return err == nil && len(it.Files) == 2
	}, "file metadata synced to nodeB")

	gotFileItem, _ := nodeB.st.GetItem(fileItem.ID)
	if !gotFileItem.NeedsRemoteFetch("dev-win") {
		t.Fatal("expected remote file item to require on-demand fetch before selection")
	}
	if gotFileItem.Files[0].OrigPath != "" {
		t.Fatalf("OrigPath must be stripped over wire, got %q", gotFileItem.Files[0].OrigPath)
	}

	// Now fetch on-demand!
	localPaths, err := nodeB.mgr.FetchRemoteFiles(context.Background(), gotFileItem, nil)
	if err != nil {
		t.Fatalf("FetchRemoteFiles: %v", err)
	}
	if len(localPaths) != 2 {
		t.Fatalf("expected 2 local paths, got %v", localPaths)
	}
	b1, err := os.ReadFile(localPaths[0])
	if err != nil || string(b1) != "file-payload-123" {
		t.Fatalf("unexpected downloaded file content: %q (%v)", string(b1), err)
	}
	b2, err := os.ReadFile(filepath.Join(localPaths[1], "nested", "inner.txt"))
	if err != nil || string(b2) != "inside-folder" {
		t.Fatalf("unexpected downloaded nested folder file: %q (%v)", string(b2), err)
	}

	// 4. Template folder & template sync
	tf := model.TemplateFolder{ID: "tf1", Name: "Snippets", UpdatedAt: 4000}
	if err := nodeA.st.UpsertTemplateFolder(tf); err != nil {
		t.Fatal(err)
	}
	nodeA.mgr.BroadcastTemplateFolderUpsert(tf)

	tp := model.TemplateItem{
		ID:             "tp1",
		FolderID:       "tf1",
		Title:          "Signature",
		Kind:           model.KindText,
		ContentHash:    clipboard.HashText("Best regards"),
		TextContent:    "Best regards",
		OriginDeviceID: "dev-mac",
		UpdatedAt:      4100,
	}
	if err := nodeA.st.UpsertTemplate(tp); err != nil {
		t.Fatal(err)
	}
	nodeA.mgr.BroadcastTemplateUpsert(tp, nil)

	waitUntil(t, 5*time.Second, func() bool {
		folders, _ := nodeB.st.ListTemplateFolders()
		tmpls, _ := nodeB.st.ListTemplates()
		return len(folders) == 1 && len(tmpls) == 1 && tmpls[0].TextContent == "Best regards"
	}, "template folder and item synced to nodeB")
}
