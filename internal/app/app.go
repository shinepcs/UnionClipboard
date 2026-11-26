package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"unionclip/internal/clipboard"
	"unionclip/internal/discovery"
	"unionclip/internal/model"
	"unionclip/internal/platform"
	"unionclip/internal/security"
	"unionclip/internal/store"
	syncpkg "unionclip/internal/sync"
	"unionclip/internal/ui"
)

type App struct {
	store      *store.Store
	fp         string
	info       model.DeviceInfo
	suppressor *clipboard.LoopSuppressor
	syncMgr    *syncpkg.Manager
	disc       *discovery.Discovery
	uiSrv      *ui.Server
	plat       platform.Platform

	cfgMu sync.RWMutex
	cfg   model.AppConfig

	ctx    context.Context
	cancel context.CancelFunc
}

func New(dataDir string, portOverride int) (*App, error) {
	st, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}
	cfg, err := st.LoadOrInitConfig()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if portOverride >= 0 {
		cfg.ListenPort = portOverride
	}

	cert, fp, err := security.LoadOrCreateDeviceCert(st.CertsDir(), cfg.DeviceID)
	if err != nil {
		_ = st.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		store:      st,
		fp:         fp,
		cfg:        cfg,
		suppressor: clipboard.NewLoopSuppressor(5 * time.Second),
		plat:       platform.New(),
		ctx:        ctx,
		cancel:     cancel,
	}
	a.info = model.DeviceInfo{
		DeviceID:        cfg.DeviceID,
		DeviceName:      cfg.DeviceName,
		OS:              runtime.GOOS,
		CertFingerprint: fp,
		Port:            cfg.ListenPort,
	}

	a.syncMgr = syncpkg.NewManager(st, cert, fp, a.info, a.getConfig, syncpkg.Callbacks{
		OnRemoteItem:        a.onRemoteItem,
		OnRemoteItemDeleted: a.onRemoteItemDeleted,
		OnTemplatesChanged:  a.onTemplatesChanged,
		OnPeersChanged:      a.onPeersChanged,
		OnImageBlobReady:    a.onImageBlobReady,
	})

	boundPort, err := a.syncMgr.Start(ctx, cfg.ListenPort)
	if err != nil {
		cancel()
		_ = st.Close()
		return nil, fmt.Errorf("start sync server: %w", err)
	}
	a.info.Port = boundPort

	a.disc = discovery.New(a.info, a.isPairedDevice, func(devs []model.DiscoveredDevice) {
		a.syncMgr.UpdateDiscoveredDevices(devs)
		if a.uiSrv != nil {
			a.uiSrv.BroadcastEvent("discovered_changed", nil)
		}
	})
	_ = a.disc.Start(ctx)

	uiSrv, err := ui.NewServer(a)
	if err != nil {
		a.Shutdown()
		return nil, err
	}
	a.uiSrv = uiSrv
	if _, err := a.uiSrv.Start(ctx); err != nil {
		a.Shutdown()
		return nil, fmt.Errorf("start viewer server: %w", err)
	}
	return a, nil
}

func (a *App) ViewerURL() string {
	return a.uiSrv.ViewerURL()
}

func (a *App) SyncPort() int {
	return a.syncMgr.ListenPort()
}

func (a *App) Run(openViewerOnStart bool) error {
	return a.plat.Run(platform.Callbacks{
		OnReady: func() {
			cfg := a.getConfig()
			_ = a.plat.RegisterHotkey(cfg.Hotkey)
			a.refreshTray()
			if openViewerOnStart {
				a.plat.OpenViewer(a.uiSrv.ViewerURL())
			}
		},
		OnClipboardChange: a.handleLocalClipboard,
		OnHotkey:          a.showPopupMenu,
		OnMenuSelectItem: func(itemID string) {
			if err := a.SelectHistoryItem(itemID, true); err != nil {
				a.plat.ShowNotification("UnionClipboard", "붙여넣기 실패: "+err.Error())
				a.uiSrv.BroadcastEvent("toast", "붙여넣기 실패: "+err.Error())
			}
		},
		OnMenuSelectTmpl: func(tmplID string) {
			if err := a.SelectTemplate(tmplID, true); err != nil {
				a.plat.ShowNotification("UnionClipboard", "템플릿 적용 실패: "+err.Error())
				a.uiSrv.BroadcastEvent("toast", "템플릿 적용 실패: "+err.Error())
			}
		},
		OnOpenViewer: func() {
			a.plat.OpenViewer(a.uiSrv.ViewerURL())
		},
		OnToggleUniversal: func() {
			cfg := a.getConfig()
			cfg.UniversalClipboard = !cfg.UniversalClipboard
			_, _ = a.UpdateConfig(cfg)
		},
		OnTogglePause: func() {
			cfg := a.getConfig()
			cfg.Paused = !cfg.Paused
			_, _ = a.UpdateConfig(cfg)
		},
		OnQuit: func() {
			a.Shutdown()
			a.plat.Quit()
		},
	})
}

func (a *App) Shutdown() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.disc != nil {
		a.disc.Stop()
	}
	if a.syncMgr != nil {
		a.syncMgr.Stop()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
}

func (a *App) getConfig() model.AppConfig {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

func (a *App) isPairedDevice(deviceID, fp string) bool {
	if p, err := a.store.GetPeer(deviceID); err == nil && (fp == "" || p.CertFingerprint == fp) {
		return true
	}
	return false
}

func (a *App) handleLocalClipboard(snap platform.ClipboardSnapshot) {
	cfg := a.getConfig()
	if cfg.Paused || snap.Sensitive {
		return
	}
	if isExcluded(snap.Foreground, cfg.ExcludedApps) {
		return
	}

	now := time.Now().UnixMilli()
	item := model.ClipboardItem{
		OriginDeviceID:   cfg.DeviceID,
		OriginDeviceName: cfg.DeviceName,
		OriginOS:         runtime.GOOS,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	var thumbBytes []byte

	switch snap.Kind {
	case model.KindText:
		if snap.Text == "" {
			return
		}
		item.Kind = model.KindText
		item.TextContent = snap.Text
		item.ContentHash = clipboard.HashText(snap.Text)
		item.ByteSize = int64(len(snap.Text))
		item.Title = clipboard.BuildTitle(model.KindText, snap.Text, 0, 0, nil)

	case model.KindImage:
		if len(snap.PNG) == 0 {
			return
		}
		pngBytes, w, h, err := clipboard.NormalizeToPNG(snap.PNG)
		if err != nil {
			return
		}
		if cfg.MaxImageBytes > 0 && int64(len(pngBytes)) > cfg.MaxImageBytes {
			return
		}
		hash := clipboard.HashBytes(pngBytes)
		item.Kind = model.KindImage
		item.ContentHash = hash
		item.ByteSize = int64(len(pngBytes))
		item.ImageWidth = w
		item.ImageHeight = h
		item.Title = clipboard.BuildTitle(model.KindImage, "", w, h, nil)

		if blobRel, err := a.store.SaveBlob(hash, pngBytes); err == nil {
			item.BlobPath = blobRel
		}
		if th, err := clipboard.MakeThumbnailPNG(pngBytes, 64, 64); err == nil {
			thumbBytes = th
			if thumbRel, err := a.store.SaveThumb(hash, th); err == nil {
				item.ThumbPath = thumbRel
			}
		}

	case model.KindFiles:
		if len(snap.FilePaths) == 0 {
			return
		}
		var entries []model.FileEntry
		var totalSize int64
		for _, p := range snap.FilePaths {
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			sz := info.Size()
			if info.IsDir() {
				sz = 0
			} else {
				totalSize += sz
			}
			entries = append(entries, model.FileEntry{
				RelativeName: filepath.Base(p),
				OrigPath:     p,
				Size:         sz,
				IsDir:        info.IsDir(),
				ModTime:      info.ModTime().UnixMilli(),
			})
		}
		if len(entries) == 0 {
			return
		}
		item.Kind = model.KindFiles
		item.Files = entries
		item.ByteSize = totalSize
		item.ContentHash = clipboard.HashFiles(entries)
		item.Title = clipboard.BuildTitle(model.KindFiles, "", 0, 0, entries)

	default:
		return
	}

	if a.suppressor.IsSuppressed(item.ContentHash) {
		return
	}

	saved, err := a.store.UpsertLocalItem(item)
	if err != nil {
		return
	}
	_ = a.store.PruneHistory(cfg.MaxHistory)

	a.syncMgr.BroadcastItemUpsert(saved, thumbBytes)
	a.uiSrv.BroadcastEvent("history_changed", nil)
}

func (a *App) onRemoteItem(item model.ClipboardItem) {
	a.uiSrv.BroadcastEvent("history_changed", nil)

	cfg := a.getConfig()
	if !cfg.UniversalClipboard || cfg.Paused {
		return
	}
	if item.Kind == model.KindText && item.TextContent != "" {
		a.suppressor.Suppress(item.ContentHash)
		_ = a.plat.WriteClipboard(platform.ClipboardPayload{
			Kind: model.KindText,
			Text: item.TextContent,
		})
	}
	// KindFiles never auto-downloads or overwrites OS clipboard until explicitly selected.
}

func (a *App) onImageBlobReady(itemID string) {
	a.uiSrv.BroadcastEvent("history_changed", nil)

	cfg := a.getConfig()
	if !cfg.UniversalClipboard || cfg.Paused {
		return
	}
	// Only mirror into system clipboard if this image is still the latest item
	list, err := a.store.ListHistory(model.HistoryFilter{Limit: 1})
	if err != nil || len(list) == 0 || list[0].ID != itemID {
		return
	}
	it := list[0]
	if it.OriginDeviceID == cfg.DeviceID || it.Kind != model.KindImage {
		return
	}
	if data, err := a.store.ReadBlobByHash(it.ContentHash); err == nil {
		a.suppressor.Suppress(it.ContentHash)
		_ = a.plat.WriteClipboard(platform.ClipboardPayload{
			Kind: model.KindImage,
			PNG:  data,
		})
	}
}

func (a *App) onRemoteItemDeleted(_ string) {
	a.uiSrv.BroadcastEvent("history_changed", nil)
}

func (a *App) onTemplatesChanged() {
	a.uiSrv.BroadcastEvent("templates_changed", nil)
}

func (a *App) onPeersChanged() {
	a.refreshTray()
	a.uiSrv.BroadcastEvent("peers_changed", nil)
}

func (a *App) showPopupMenu() {
	cfg := a.getConfig()
	items, _ := a.store.ListHistory(model.HistoryFilter{Limit: 25})
	folders, tmpls, _ := a.ListTemplates()

	var menuHist []platform.PopupMenuItem
	for _, it := range items {
		label := it.Title
		if it.OriginDeviceID != cfg.DeviceID {
			tag := "[Win]"
			if it.OriginOS == "darwin" {
				tag = "[Mac]"
			}
			if it.NeedsRemoteFetch(cfg.DeviceID) {
				tag += " ☁"
			}
			label = tag + " " + label
		}
		var thumb []byte
		if it.Kind == model.KindImage && it.ContentHash != "" {
			thumb, _ = a.store.ReadThumbByHash(it.ContentHash)
		}
		menuHist = append(menuHist, platform.PopupMenuItem{
			ID:       it.ID,
			Label:    label,
			ThumbPNG: thumb,
		})
	}

	folderMap := make(map[string][]platform.PopupMenuItem)
	var rootTmpls []platform.PopupMenuItem
	for _, t := range tmpls {
		var thumb []byte
		if t.Kind == model.KindImage && t.ContentHash != "" {
			thumb, _ = a.store.ReadThumbByHash(t.ContentHash)
		}
		pm := platform.PopupMenuItem{
			ID:       t.ID,
			Label:    t.Title,
			ThumbPNG: thumb,
		}
		if t.FolderID == "" {
			rootTmpls = append(rootTmpls, pm)
		} else {
			folderMap[t.FolderID] = append(folderMap[t.FolderID], pm)
		}
	}

	var menuFolders []platform.PopupMenuFolder
	for _, f := range folders {
		menuFolders = append(menuFolders, platform.PopupMenuFolder{
			Title: f.Name,
			Items: folderMap[f.ID],
		})
	}

	a.plat.ShowPopupMenu(platform.PopupMenuModel{
		History:            menuHist,
		Templates:          rootTmpls,
		TemplateFolders:    menuFolders,
		UniversalClipboard: cfg.UniversalClipboard,
		Paused:             cfg.Paused,
		PeerCount:          len(a.syncMgr.OnlinePeerIDs()),
	})
}

func (a *App) refreshTray() {
	cfg := a.getConfig()
	peers, _ := a.store.ListPeers()
	onlineMap := a.syncMgr.OnlinePeerIDs()
	onlineCnt := 0
	for _, p := range peers {
		if onlineMap[p.DeviceID] {
			onlineCnt++
		}
	}
	a.plat.UpdateTray(platform.TrayState{
		OnlinePeers:        onlineCnt,
		TotalPeers:         len(peers),
		UniversalClipboard: cfg.UniversalClipboard,
		Paused:             cfg.Paused,
		Hotkey:             cfg.Hotkey,
	})
}

// ui.Backend implementation

func (a *App) UIState() ui.UIState {
	cfg := a.getConfig()
	peers, _ := a.store.ListPeers()
	online := a.syncMgr.OnlinePeerIDs()
	for i := range peers {
		peers[i].Online = online[peers[i].DeviceID]
	}
	cacheSz, _ := a.store.ReceivedCacheSize()
	return ui.UIState{
		Device:               a.info,
		Config:               cfg,
		Peers:                peers,
		Discovered:           a.disc.List(),
		AccessibilityGranted: a.plat.HasAccessibilityPermission(),
		CacheBytes:           cacheSz,
		OS:                   runtime.GOOS,
	}
}

func (a *App) ListHistory(filter model.HistoryFilter) ([]model.ClipboardItem, error) {
	return a.store.ListHistory(filter)
}

func (a *App) SelectHistoryItem(id string, paste bool) error {
	it, err := a.store.GetItem(id)
	if err != nil {
		return err
	}
	if it.DeletedAt > 0 {
		return errors.New("item has been deleted")
	}
	cfg := a.getConfig()

	var payload platform.ClipboardPayload
	payload.Kind = it.Kind

	switch it.Kind {
	case model.KindText:
		payload.Text = it.TextContent
	case model.KindImage:
		data, err := a.store.ReadBlobByHash(it.ContentHash)
		if err != nil && it.OriginDeviceID != cfg.DeviceID {
			ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
			err = a.syncMgr.EnsureBlob(ctx, it.OriginDeviceID, it.ContentHash)
			cancel()
			if err == nil {
				data, err = a.store.ReadBlobByHash(it.ContentHash)
			}
		}
		if err != nil {
			return fmt.Errorf("image data unavailable: %w", err)
		}
		payload.PNG = data
	case model.KindFiles:
		if it.NeedsRemoteFetch(cfg.DeviceID) {
			a.plat.ShowNotification("UnionClipboard", fmt.Sprintf("원격 파일 수신 중: %s", it.Title))
			ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
			paths, err := a.syncMgr.FetchRemoteFiles(ctx, it, func(ev model.FileProgressEvent) {
				a.uiSrv.BroadcastEvent("download_progress", ev)
			})
			cancel()
			if err != nil {
				return err
			}
			payload.FilePaths = paths
			a.uiSrv.BroadcastEvent("history_changed", nil)
		} else {
			payload.FilePaths = it.LocalPaths(cfg.DeviceID)
		}
		if len(payload.FilePaths) == 0 {
			return errors.New("no accessible local files")
		}
	}

	a.suppressor.Suppress(it.ContentHash)
	if err := a.plat.WriteClipboard(payload); err != nil {
		return err
	}
	if paste && cfg.AutoPaste {
		a.plat.SendPaste()
	}
	return nil
}

func (a *App) UpdateHistoryText(id, text string) (model.ClipboardItem, error) {
	hash := clipboard.HashText(text)
	title := clipboard.BuildTitle(model.KindText, text, 0, 0, nil)
	updated, err := a.store.UpdateItemText(id, text, title, hash, time.Now().UnixMilli())
	if err != nil {
		return updated, err
	}
	a.syncMgr.BroadcastItemUpsert(updated, nil)
	a.uiSrv.BroadcastEvent("history_changed", nil)
	return updated, nil
}

func (a *App) DeleteHistoryItem(id string) error {
	now := time.Now().UnixMilli()
	if err := a.store.SoftDeleteItem(id, now); err != nil {
		return err
	}
	a.syncMgr.BroadcastItemDelete(id, now)
	a.uiSrv.BroadcastEvent("history_changed", nil)
	return nil
}

func (a *App) ClearHistory() error {
	now := time.Now().UnixMilli()
	ids, err := a.store.ClearHistory(now)
	if err != nil {
		return err
	}
	for _, id := range ids {
		a.syncMgr.BroadcastItemDelete(id, now)
	}
	_ = a.store.GCBlobs()
	a.uiSrv.BroadcastEvent("history_changed", nil)
	return nil
}

func (a *App) SaveItemAsTemplate(itemID, folderID, title string) (model.TemplateItem, error) {
	it, err := a.store.GetItem(itemID)
	if err != nil {
		return model.TemplateItem{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = it.Title
	}
	cfg := a.getConfig()
	tmpl := model.TemplateItem{
		ID:             uuid.NewString(),
		FolderID:       folderID,
		Title:          title,
		Kind:           it.Kind,
		ContentHash:    it.ContentHash,
		TextContent:    it.TextContent,
		BlobPath:       it.BlobPath,
		ThumbPath:      it.ThumbPath,
		ByteSize:       it.ByteSize,
		ImageWidth:     it.ImageWidth,
		ImageHeight:    it.ImageHeight,
		Files:          it.Files,
		OriginDeviceID: cfg.DeviceID,
		UpdatedAt:      time.Now().UnixMilli(),
	}
	if err := a.store.UpsertTemplate(tmpl); err != nil {
		return tmpl, err
	}
	var thumb []byte
	if tmpl.Kind == model.KindImage && tmpl.ContentHash != "" {
		thumb, _ = a.store.ReadThumbByHash(tmpl.ContentHash)
	}
	a.syncMgr.BroadcastTemplateUpsert(tmpl, thumb)
	a.uiSrv.BroadcastEvent("templates_changed", nil)
	return tmpl, nil
}

func (a *App) ListTemplates() ([]model.TemplateFolder, []model.TemplateItem, error) {
	folders, err := a.store.ListTemplateFolders()
	if err != nil {
		return nil, nil, err
	}
	items, err := a.store.ListTemplates()
	return folders, items, err
}

func (a *App) UpsertTemplateFolder(f model.TemplateFolder) (model.TemplateFolder, error) {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	f.UpdatedAt = time.Now().UnixMilli()
	if err := a.store.UpsertTemplateFolder(f); err != nil {
		return f, err
	}
	a.syncMgr.BroadcastTemplateFolderUpsert(f)
	a.uiSrv.BroadcastEvent("templates_changed", nil)
	return f, nil
}

func (a *App) DeleteTemplateFolder(id string) error {
	now := time.Now().UnixMilli()
	if err := a.store.SoftDeleteTemplateFolder(id, now); err != nil {
		return err
	}
	a.syncMgr.BroadcastTemplateFolderDelete(id, now)
	a.uiSrv.BroadcastEvent("templates_changed", nil)
	return nil
}

func (a *App) UpsertTemplate(t model.TemplateItem) (model.TemplateItem, error) {
	cfg := a.getConfig()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Kind == "" {
		t.Kind = model.KindText
	}
	if t.OriginDeviceID == "" {
		t.OriginDeviceID = cfg.DeviceID
	}
	if t.Kind == model.KindText {
		t.ContentHash = clipboard.HashText(t.TextContent)
		t.ByteSize = int64(len(t.TextContent))
	}
	t.UpdatedAt = time.Now().UnixMilli()
	if err := a.store.UpsertTemplate(t); err != nil {
		return t, err
	}
	var thumb []byte
	if t.Kind == model.KindImage && t.ContentHash != "" {
		thumb, _ = a.store.ReadThumbByHash(t.ContentHash)
	}
	a.syncMgr.BroadcastTemplateUpsert(t, thumb)
	a.uiSrv.BroadcastEvent("templates_changed", nil)
	return t, nil
}

func (a *App) DeleteTemplate(id string) error {
	now := time.Now().UnixMilli()
	if err := a.store.SoftDeleteTemplate(id, now); err != nil {
		return err
	}
	a.syncMgr.BroadcastTemplateDelete(id, now)
	a.uiSrv.BroadcastEvent("templates_changed", nil)
	return nil
}

func (a *App) SelectTemplate(id string, paste bool) error {
	t, err := a.store.GetTemplate(id)
	if err != nil {
		return err
	}
	if t.DeletedAt > 0 {
		return errors.New("template deleted")
	}
	cfg := a.getConfig()
	var payload platform.ClipboardPayload
	payload.Kind = t.Kind

	switch t.Kind {
	case model.KindText:
		payload.Text = t.TextContent
	case model.KindImage:
		data, err := a.store.ReadBlobByHash(t.ContentHash)
		if err != nil && t.OriginDeviceID != cfg.DeviceID {
			ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
			err = a.syncMgr.EnsureBlob(ctx, t.OriginDeviceID, t.ContentHash)
			cancel()
			if err == nil {
				data, err = a.store.ReadBlobByHash(t.ContentHash)
			}
		}
		if err != nil {
			return err
		}
		payload.PNG = data
	case model.KindFiles:
		for _, f := range t.Files {
			if f.OrigPath != "" {
				payload.FilePaths = append(payload.FilePaths, f.OrigPath)
			} else if f.LocalCachedPath != "" {
				payload.FilePaths = append(payload.FilePaths, f.LocalCachedPath)
			}
		}
		if len(payload.FilePaths) == 0 && t.OriginDeviceID != cfg.DeviceID {
			tmpItem := model.ClipboardItem{
				ID:             t.ID,
				Kind:           model.KindFiles,
				Files:          t.Files,
				OriginDeviceID: t.OriginDeviceID,
			}
			ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
			paths, fErr := a.syncMgr.FetchRemoteFiles(ctx, tmpItem, nil)
			cancel()
			if fErr != nil {
				return fErr
			}
			payload.FilePaths = paths
		}
	}

	a.suppressor.Suppress(t.ContentHash)
	if err := a.plat.WriteClipboard(payload); err != nil {
		return err
	}
	if paste && cfg.AutoPaste {
		a.plat.SendPaste()
	}
	return nil
}

func (a *App) StartPairing() (string, int64, error) {
	return a.syncMgr.Pairing().StartSession()
}

func (a *App) ConnectWithPIN(addr, pin string) (model.Peer, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	return a.syncMgr.PairWithRemote(ctx, addr, pin)
}

func (a *App) UpdatePeerAddr(id, addr string) error {
	if err := a.store.UpdatePeerManualAddr(id, addr); err != nil {
		return err
	}
	a.syncMgr.TriggerConnect(id)
	a.uiSrv.BroadcastEvent("peers_changed", nil)
	return nil
}

func (a *App) RemovePeer(id string) error {
	if err := a.store.DeletePeer(id); err != nil {
		return err
	}
	a.refreshTray()
	a.uiSrv.BroadcastEvent("peers_changed", nil)
	return nil
}

func (a *App) UpdateConfig(next model.AppConfig) (model.AppConfig, error) {
	a.cfgMu.Lock()
	prevHotkey := a.cfg.Hotkey
	next.DeviceID = a.cfg.DeviceID
	next.ListenPort = a.cfg.ListenPort
	if next.MaxHistory <= 0 {
		next.MaxHistory = 200
	}
	if next.MaxImageBytes <= 0 {
		next.MaxImageBytes = 20 * 1024 * 1024
	}
	if next.MaxFileDownloadBytes <= 0 {
		next.MaxFileDownloadBytes = 2 * 1024 * 1024 * 1024
	}
	a.cfg = next
	a.info.DeviceName = next.DeviceName
	a.cfgMu.Unlock()

	if err := a.store.SaveConfig(next); err != nil {
		return next, err
	}
	if next.Hotkey != prevHotkey {
		if err := a.plat.RegisterHotkey(next.Hotkey); err != nil {
			return next, err
		}
	}
	a.refreshTray()
	a.uiSrv.BroadcastEvent("config_changed", nil)
	return next, nil
}

func (a *App) ClearCache() error {
	if err := a.store.ClearReceivedFileCache(); err != nil {
		return err
	}
	a.uiSrv.BroadcastEvent("config_changed", nil)
	a.uiSrv.BroadcastEvent("history_changed", nil)
	return nil
}

func (a *App) RequestAccessibility() {
	a.plat.RequestAccessibilityPermission()
}

func (a *App) RevealItemFiles(id string) error {
	it, err := a.store.GetItem(id)
	if err != nil {
		return err
	}
	cfg := a.getConfig()
	paths := it.LocalPaths(cfg.DeviceID)
	if len(paths) == 0 {
		return errors.New("no local files to reveal")
	}
	return a.plat.RevealPaths(paths)
}

func (a *App) ReadBlob(sha256Hex string) ([]byte, error) {
	return a.store.ReadBlobByHash(sha256Hex)
}

func (a *App) ReadThumb(sha256Hex string) ([]byte, error) {
	return a.store.ReadThumbByHash(sha256Hex)
}

func isExcluded(fg string, list []string) bool {
	if fg == "" {
		return false
	}
	low := strings.ToLower(fg)
	for _, ex := range list {
		if ex != "" && strings.EqualFold(low, strings.TrimSpace(ex)) {
			return true
		}
	}
	return false
}
