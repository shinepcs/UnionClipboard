package sync

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"unionclip/internal/model"
	"unionclip/internal/security"
	"unionclip/internal/store"
)

type Callbacks struct {
	OnRemoteItem         func(item model.ClipboardItem)
	OnRemoteItemDeleted  func(id string)
	OnTemplatesChanged   func()
	OnPeersChanged       func()
	OnImageBlobReady     func(itemID string)
}

type peerConn struct {
	peerID string
	ws     *websocket.Conn
	mu     sync.Mutex
}

func (pc *peerConn) sendJSON(v any) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	_ = pc.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return pc.ws.WriteJSON(v)
}

type Manager struct {
	store     *store.Store
	cert      tls.Certificate
	fp        string
	info      model.DeviceInfo
	pairing   *security.PairingManager
	getConfig func() model.AppConfig
	callbacks Callbacks

	bindHost string
	ln       net.Listener
	httpSrv  *http.Server

	mu             sync.RWMutex
	conns          map[string]*peerConn
	discoveredAddr map[string]string
	dialWakeup     map[string]chan struct{}
	rootCtx        context.Context
	cancel         context.CancelFunc
}

func NewManager(
	st *store.Store,
	cert tls.Certificate,
	fp string,
	info model.DeviceInfo,
	getConfig func() model.AppConfig,
	cb Callbacks,
) *Manager {
	info.CertFingerprint = fp
	return &Manager{
		store:          st,
		cert:           cert,
		fp:             fp,
		info:           info,
		pairing:        security.NewPairingManager(info),
		getConfig:      getConfig,
		callbacks:      cb,
		conns:          make(map[string]*peerConn),
		discoveredAddr: make(map[string]string),
		dialWakeup:     make(map[string]chan struct{}),
	}
}

func (m *Manager) SetBindHost(host string) {
	m.bindHost = host
}

func (m *Manager) Pairing() *security.PairingManager {
	return m.pairing
}

func (m *Manager) Start(ctx context.Context, port int) (int, error) {
	m.rootCtx, m.cancel = context.WithCancel(ctx)

	addr := net.JoinHostPort(m.bindHost, fmt.Sprintf("%d", port))
	rawLn, err := net.Listen("tcp", addr)
	if err != nil && port != 0 {
		// Fall back to ephemeral port if default port is already taken
		rawLn, err = net.Listen("tcp", net.JoinHostPort(m.bindHost, "0"))
	}
	if err != nil {
		return 0, err
	}
	boundPort := rawLn.Addr().(*net.TCPAddr).Port
	m.info.Port = boundPort
	m.pairing = security.NewPairingManager(m.info)

	tlsLn := tls.NewListener(rawLn, security.ServerTLSConfig(m.cert))
	m.ln = tlsLn
	m.httpSrv = &http.Server{
		Handler:           m.buildHTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		_ = m.httpSrv.Serve(tlsLn)
	}()

	go m.dialLoop(m.rootCtx)
	return boundPort, nil
}

func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.httpSrv != nil {
		_ = m.httpSrv.Close()
	}
	m.mu.Lock()
	for id, pc := range m.conns {
		_ = pc.ws.Close()
		delete(m.conns, id)
	}
	m.mu.Unlock()
}

func (m *Manager) ListenPort() int {
	return m.info.Port
}

func (m *Manager) EnsureBlob(ctx context.Context, peerID, sha256Hex string) error {
	return m.fetchBlobFromPeer(ctx, peerID, sha256Hex)
}

func (m *Manager) UpdateDiscoveredDevices(list []model.DiscoveredDevice) {
	m.mu.Lock()
	for _, d := range list {
		if d.Addr != "" {
			m.discoveredAddr[d.DeviceID] = d.Addr
		}
	}
	m.mu.Unlock()

	for _, d := range list {
		if d.AlreadyPaired {
			m.TriggerConnect(d.DeviceID)
		}
	}
}

func (m *Manager) TriggerConnect(peerID string) {
	m.mu.RLock()
	ch := m.dialWakeup[peerID]
	m.mu.RUnlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) IsPeerOnline(peerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.conns[peerID]
	return ok
}

func (m *Manager) OnlinePeerIDs() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]bool, len(m.conns))
	for k := range m.conns {
		out[k] = true
	}
	return out
}

func (m *Manager) BroadcastItemUpsert(item model.ClipboardItem, thumbPNG []byte) {
	sanitized := item.SanitizedForSync()
	msg := model.SyncMessage{
		Type:     "item_upsert",
		SenderID: m.info.DeviceID,
		SentAt:   time.Now().UnixMilli(),
		Item:     &sanitized,
		ThumbPNG: thumbPNG,
	}
	m.broadcast(msg)
}

func (m *Manager) BroadcastItemDelete(id string, deletedAt int64) {
	m.broadcast(model.SyncMessage{
		Type:      "item_delete",
		SenderID:  m.info.DeviceID,
		SentAt:    time.Now().UnixMilli(),
		ID:        id,
		DeletedAt: deletedAt,
	})
}

func (m *Manager) BroadcastTemplateFolderUpsert(f model.TemplateFolder) {
	m.broadcast(model.SyncMessage{
		Type:     "template_folder_upsert",
		SenderID: m.info.DeviceID,
		SentAt:   time.Now().UnixMilli(),
		Folder:   &f,
	})
}

func (m *Manager) BroadcastTemplateFolderDelete(id string, deletedAt int64) {
	m.broadcast(model.SyncMessage{
		Type:      "template_folder_delete",
		SenderID:  m.info.DeviceID,
		SentAt:    time.Now().UnixMilli(),
		ID:        id,
		DeletedAt: deletedAt,
	})
}

func (m *Manager) BroadcastTemplateUpsert(t model.TemplateItem, thumbPNG []byte) {
	sanitized := t.SanitizedForSync()
	m.broadcast(model.SyncMessage{
		Type:     "template_upsert",
		SenderID: m.info.DeviceID,
		SentAt:   time.Now().UnixMilli(),
		Template: &sanitized,
		ThumbPNG: thumbPNG,
	})
}

func (m *Manager) BroadcastTemplateDelete(id string, deletedAt int64) {
	m.broadcast(model.SyncMessage{
		Type:      "template_delete",
		SenderID:  m.info.DeviceID,
		SentAt:    time.Now().UnixMilli(),
		ID:        id,
		DeletedAt: deletedAt,
	})
}

func (m *Manager) broadcast(msg model.SyncMessage) {
	m.mu.RLock()
	targets := make([]*peerConn, 0, len(m.conns))
	for _, pc := range m.conns {
		targets = append(targets, pc)
	}
	m.mu.RUnlock()

	for _, pc := range targets {
		_ = pc.sendJSON(msg)
	}
}

func (m *Manager) dialLoop(ctx context.Context) {
	workers := make(map[string]context.CancelFunc)
	checkPeers := func() {
		peers, err := m.store.ListPeers()
		if err != nil {
			return
		}
		activeSet := make(map[string]bool)
		for _, p := range peers {
			activeSet[p.DeviceID] = true
			if _, ok := workers[p.DeviceID]; !ok {
				pCtx, cancel := context.WithCancel(ctx)
				workers[p.DeviceID] = cancel
				ch := make(chan struct{}, 1)
				m.mu.Lock()
				m.dialWakeup[p.DeviceID] = ch
				m.mu.Unlock()
				go m.peerDialWorker(pCtx, p.DeviceID, ch)
			}
		}
		for id, cancel := range workers {
			if !activeSet[id] {
				cancel()
				delete(workers, id)
				m.mu.Lock()
				delete(m.dialWakeup, id)
				if pc, ok := m.conns[id]; ok {
					_ = pc.ws.Close()
					delete(m.conns, id)
				}
				m.mu.Unlock()
			}
		}
	}

	checkPeers()
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkPeers()
		}
	}
}

func (m *Manager) peerDialWorker(ctx context.Context, peerID string, wakeup <-chan struct{}) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if !m.IsPeerOnline(peerID) {
			peer, addr, err := m.resolvePeerAddr(peerID)
			// Deterministic tie-breaker: both sides can dial if offline; servePeerConn handles collision cleanly
			if err == nil && addr != "" {
				ws, dErr := m.dialPeerWS(ctx, peer, addr)
				if dErr == nil {
					backoff = time.Second
					m.servePeerConn(peer, ws, true)
					continue
				}
			}
		}

		wait := backoff
		if backoff < 20*time.Second {
			backoff *= 2
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wakeup:
			timer.Stop()
			backoff = time.Second
		case <-timer.C:
		}
	}
}

func (m *Manager) resolvePeerAddr(peerID string) (model.Peer, string, error) {
	peer, err := m.store.GetPeer(peerID)
	if err != nil {
		return model.Peer{}, "", err
	}
	if peer.ManualAddr != "" {
		return peer, peer.ManualAddr, nil
	}
	m.mu.RLock()
	disc := m.discoveredAddr[peerID]
	m.mu.RUnlock()
	if disc != "" {
		return peer, disc, nil
	}
	if peer.LastAddr != "" {
		return peer, peer.LastAddr, nil
	}
	return peer, "", errors.New("no known address for peer")
}

func (m *Manager) servePeerConn(peer model.Peer, ws *websocket.Conn, isDialer bool) {
	pc := &peerConn{peerID: peer.DeviceID, ws: ws}

	m.mu.Lock()
	if prev, ok := m.conns[peer.DeviceID]; ok && prev != pc {
		_ = prev.ws.Close()
	}
	m.conns[peer.DeviceID] = pc
	m.mu.Unlock()

	m.notifyPeersChanged()
	defer func() {
		_ = ws.Close()
		m.mu.Lock()
		if cur, ok := m.conns[peer.DeviceID]; ok && cur == pc {
			delete(m.conns, peer.DeviceID)
		}
		m.mu.Unlock()
		m.notifyPeersChanged()
	}()

	_ = ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(45 * time.Second))
		return nil
	})

	// Send hello + delta request
	_ = pc.sendJSON(model.SyncMessage{
		Type:       "hello",
		SenderID:   m.info.DeviceID,
		SentAt:     time.Now().UnixMilli(),
		DeviceInfo: &m.info,
	})
	_ = pc.sendJSON(model.SyncMessage{
		Type:     "delta_request",
		SenderID: m.info.DeviceID,
		SentAt:   time.Now().UnixMilli(),
		DeltaReq: &model.DeltaRequest{SinceMs: peer.LastSyncAt},
	})

	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ticker.C:
				pc.mu.Lock()
				_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := ws.WriteMessage(websocket.PingMessage, nil)
				pc.mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	_ = isDialer
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(45 * time.Second))
		var msg model.SyncMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		m.handleSyncMessage(peer, pc, msg)
	}
}

func (m *Manager) handleSyncMessage(peer model.Peer, pc *peerConn, msg model.SyncMessage) {
	switch msg.Type {
	case "hello":
		if msg.DeviceInfo != nil {
			_ = m.store.UpdatePeerSeen(peer.DeviceID, "", time.Now().UnixMilli())
		}
	case "delta_request":
		since := int64(0)
		if msg.DeltaReq != nil {
			since = msg.DeltaReq.SinceMs
		}
		resp, err := m.store.ListChangesSince(since)
		if err != nil {
			return
		}
		for i := range resp.Items {
			if resp.Items[i].Kind == model.KindImage && store.IsValidSHA256(resp.Items[i].ContentHash) {
				if th, tErr := m.store.ReadThumbByHash(resp.Items[i].ContentHash); tErr == nil {
					resp.Thumbs[resp.Items[i].ContentHash] = th
				}
			}
			resp.Items[i] = resp.Items[i].SanitizedForSync()
		}
		for i := range resp.Templates {
			if resp.Templates[i].Kind == model.KindImage && store.IsValidSHA256(resp.Templates[i].ContentHash) {
				if th, tErr := m.store.ReadThumbByHash(resp.Templates[i].ContentHash); tErr == nil {
					resp.Thumbs[resp.Templates[i].ContentHash] = th
				}
			}
			resp.Templates[i] = resp.Templates[i].SanitizedForSync()
		}
		_ = pc.sendJSON(model.SyncMessage{
			Type:      "delta_response",
			SenderID:  m.info.DeviceID,
			SentAt:    time.Now().UnixMilli(),
			DeltaResp: &resp,
		})
	case "delta_response":
		if msg.DeltaResp == nil {
			return
		}
		for hash, th := range msg.DeltaResp.Thumbs {
			_, _ = m.store.SaveThumb(hash, th)
		}
		for _, it := range msg.DeltaResp.Items {
			m.applyRemoteItem(peer.DeviceID, it, nil, false)
		}
		for _, f := range msg.DeltaResp.Folders {
			_ = m.store.UpsertTemplateFolder(f)
		}
		for _, t := range msg.DeltaResp.Templates {
			m.applyRemoteTemplate(peer.DeviceID, t, nil)
		}
		if len(msg.DeltaResp.Folders) > 0 || len(msg.DeltaResp.Templates) > 0 {
			if m.callbacks.OnTemplatesChanged != nil {
				m.callbacks.OnTemplatesChanged()
			}
		}
		_ = m.store.UpdatePeerLastSync(peer.DeviceID, msg.DeltaResp.ServerTimeMs)
	case "item_upsert":
		if msg.Item != nil {
			m.applyRemoteItem(peer.DeviceID, *msg.Item, msg.ThumbPNG, true)
			_ = m.store.UpdatePeerLastSync(peer.DeviceID, msg.SentAt)
		}
	case "item_delete":
		if msg.ID != "" {
			_ = m.store.SoftDeleteItem(msg.ID, msg.DeletedAt)
			if m.callbacks.OnRemoteItemDeleted != nil {
				m.callbacks.OnRemoteItemDeleted(msg.ID)
			}
			_ = m.store.UpdatePeerLastSync(peer.DeviceID, msg.SentAt)
		}
	case "template_folder_upsert":
		if msg.Folder != nil {
			_ = m.store.UpsertTemplateFolder(*msg.Folder)
			if m.callbacks.OnTemplatesChanged != nil {
				m.callbacks.OnTemplatesChanged()
			}
		}
	case "template_folder_delete":
		if msg.ID != "" {
			_ = m.store.SoftDeleteTemplateFolder(msg.ID, msg.DeletedAt)
			if m.callbacks.OnTemplatesChanged != nil {
				m.callbacks.OnTemplatesChanged()
			}
		}
	case "template_upsert":
		if msg.Template != nil {
			m.applyRemoteTemplate(peer.DeviceID, *msg.Template, msg.ThumbPNG)
			if m.callbacks.OnTemplatesChanged != nil {
				m.callbacks.OnTemplatesChanged()
			}
		}
	case "template_delete":
		if msg.ID != "" {
			_ = m.store.SoftDeleteTemplate(msg.ID, msg.DeletedAt)
			if m.callbacks.OnTemplatesChanged != nil {
				m.callbacks.OnTemplatesChanged()
			}
		}
	}
}

func (m *Manager) applyRemoteItem(fromPeerID string, item model.ClipboardItem, thumbPNG []byte, isRealtime bool) {
	if item.OriginDeviceID == m.info.DeviceID && item.DeletedAt == 0 {
		return
	}
	item = item.SanitizedForSync()
	if item.Kind == model.KindImage && store.IsValidSHA256(item.ContentHash) {
		if len(thumbPNG) > 0 {
			if rel, err := m.store.SaveThumb(item.ContentHash, thumbPNG); err == nil {
				item.ThumbPath = rel
			}
		} else if m.store.HasThumb(item.ContentHash) {
			item.ThumbPath = "thumbs/" + item.ContentHash + "_thumb.png"
		}
		if m.store.HasBlob(item.ContentHash) {
			item.BlobPath = "blobs/" + item.ContentHash + ".png"
		}
	}

	changed, err := m.store.UpsertRemoteItem(item)
	if err != nil || !changed {
		return
	}

	if item.DeletedAt > 0 {
		if m.callbacks.OnRemoteItemDeleted != nil {
			m.callbacks.OnRemoteItemDeleted(item.ID)
		}
		return
	}

	// Download full image blob asynchronously
	if item.Kind == model.KindImage && !m.store.HasBlob(item.ContentHash) {
		go func(it model.ClipboardItem, peerID string) {
			ctx, cancel := context.WithTimeout(m.rootCtx, 30*time.Second)
			defer cancel()
			if err := m.fetchBlobFromPeer(ctx, peerID, it.ContentHash); err == nil {
				_ = m.store.UpdateItemMediaPaths(it.ID, "blobs/"+it.ContentHash+".png", "")
				if m.callbacks.OnImageBlobReady != nil {
					m.callbacks.OnImageBlobReady(it.ID)
				}
			}
		}(item, fromPeerID)
	}

	if isRealtime && m.callbacks.OnRemoteItem != nil {
		m.callbacks.OnRemoteItem(item)
	}
}

func (m *Manager) applyRemoteTemplate(fromPeerID string, tmpl model.TemplateItem, thumbPNG []byte) {
	tmpl = tmpl.SanitizedForSync()
	if tmpl.Kind == model.KindImage && store.IsValidSHA256(tmpl.ContentHash) {
		if len(thumbPNG) > 0 {
			if rel, err := m.store.SaveThumb(tmpl.ContentHash, thumbPNG); err == nil {
				tmpl.ThumbPath = rel
			}
		} else if m.store.HasThumb(tmpl.ContentHash) {
			tmpl.ThumbPath = "thumbs/" + tmpl.ContentHash + "_thumb.png"
		}
		if m.store.HasBlob(tmpl.ContentHash) {
			tmpl.BlobPath = "blobs/" + tmpl.ContentHash + ".png"
		}
	}
	if err := m.store.UpsertTemplate(tmpl); err != nil {
		return
	}
	if tmpl.DeletedAt == 0 && tmpl.Kind == model.KindImage && !m.store.HasBlob(tmpl.ContentHash) {
		go func(t model.TemplateItem, peerID string) {
			ctx, cancel := context.WithTimeout(m.rootCtx, 30*time.Second)
			defer cancel()
			if err := m.fetchBlobFromPeer(ctx, peerID, t.ContentHash); err == nil {
				_ = m.store.UpdateTemplateMediaPaths(t.ID, "blobs/"+t.ContentHash+".png", "")
				if m.callbacks.OnTemplatesChanged != nil {
					m.callbacks.OnTemplatesChanged()
				}
			}
		}(tmpl, fromPeerID)
	}
}

func (m *Manager) notifyPeersChanged() {
	if m.callbacks.OnPeersChanged != nil {
		m.callbacks.OnPeersChanged()
	}
}

func (m *Manager) maxImageBytes() int64 {
	if m.getConfig != nil {
		if v := m.getConfig().MaxImageBytes; v > 0 {
			return v
		}
	}
	return 20 * 1024 * 1024
}

func (m *Manager) maxFileBytes() int64 {
	if m.getConfig != nil {
		if v := m.getConfig().MaxFileDownloadBytes; v > 0 {
			return v
		}
	}
	return 2 * 1024 * 1024 * 1024
}
