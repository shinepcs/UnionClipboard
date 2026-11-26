package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"unionclip/internal/model"
	"unionclip/internal/security"
	"unionclip/internal/store"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

type contextKey string

const ctxPeerKey contextKey = "mtls_peer"

func (m *Manager) buildHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pair/init", m.handlePairInit)
	mux.HandleFunc("/pair/confirm", m.handlePairConfirm)
	mux.HandleFunc("/api/v1/sync/stream", m.requireMTLS(m.handleSyncStream))
	mux.HandleFunc("/api/v1/blobs/", m.requireMTLS(m.handleGetBlob))
	mux.HandleFunc("/api/v1/files/", m.requireMTLS(m.handleGetFile))
	return mux
}

func (m *Manager) requireMTLS(next func(http.ResponseWriter, *http.Request, model.Peer)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusForbidden)
			return
		}
		fp := security.CertFingerprint(r.TLS.PeerCertificates[0])
		peer, err := m.store.GetPeerByFingerprint(fp)
		if err != nil {
			http.Error(w, "untrusted peer certificate", http.StatusForbidden)
			return
		}
		next(w, r, peer)
	}
}

func (m *Manager) handlePairInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusForbidden)
		return
	}
	var req security.PairInitRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.ClientCertFP = security.CertFingerprint(r.TLS.PeerCertificates[0])

	resp, err := m.pairing.InitChallenge(req)
	if err != nil {
		status := http.StatusBadRequest
		if err == security.ErrNoActivePairing || err == security.ErrPairingExpired {
			status = http.StatusConflict
		} else if err == security.ErrTooManyAttempts {
			status = http.StatusTooManyRequests
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (m *Manager) handlePairConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusForbidden)
		return
	}
	var req security.PairConfirmRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.ClientFP = security.CertFingerprint(r.TLS.PeerCertificates[0])
	req.ClientAddr = extractPeerListenAddr(r.RemoteAddr, 0)

	resp, peer, err := m.pairing.ConfirmChallenge(req)
	if err != nil {
		status := http.StatusForbidden
		if err == security.ErrTooManyAttempts {
			status = http.StatusTooManyRequests
		}
		http.Error(w, err.Error(), status)
		return
	}
	if err := m.store.UpsertPeer(peer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.notifyPeersChanged()
	writeJSON(w, http.StatusOK, resp)
}

func (m *Manager) handleSyncStream(w http.ResponseWriter, r *http.Request, peer model.Peer) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	remoteHost := extractPeerHost(r.RemoteAddr)
	if remoteHost != "" && r.URL.Query().Get("port") != "" {
		if p, err := strconv.Atoi(r.URL.Query().Get("port")); err == nil && p > 0 {
			_ = m.store.UpdatePeerSeen(peer.DeviceID, net.JoinHostPort(remoteHost, strconv.Itoa(p)), time.Now().UnixMilli())
		}
	}
	m.servePeerConn(peer, conn, false)
}

func (m *Manager) handleGetBlob(w http.ResponseWriter, r *http.Request, _ model.Peer) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sha := strings.TrimPrefix(r.URL.Path, "/api/v1/blobs/")
	if !store.IsValidSHA256(sha) {
		http.Error(w, "invalid blob hash", http.StatusBadRequest)
		return
	}
	data, err := m.store.ReadBlobByHash(sha)
	if err != nil {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func (m *Manager) handleGetFile(w http.ResponseWriter, r *http.Request, _ model.Peer) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/files/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "invalid file request", http.StatusBadRequest)
		return
	}
	itemID := parts[0]
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		http.Error(w, "invalid file index", http.StatusBadRequest)
		return
	}

	var files []model.FileEntry
	var kind model.ItemKind
	var deletedAt int64

	if item, err := m.store.GetItem(itemID); err == nil {
		files = item.Files
		kind = item.Kind
		deletedAt = item.DeletedAt
	} else if tmpl, err := m.store.GetTemplate(itemID); err == nil {
		files = tmpl.Files
		kind = tmpl.Kind
		deletedAt = tmpl.DeletedAt
	} else {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}

	if deletedAt > 0 || kind != model.KindFiles || idx >= len(files) {
		http.Error(w, "file entry not available", http.StatusNotFound)
		return
	}
	entry := files[idx]
	localPath := entry.OrigPath
	if localPath == "" {
		localPath = entry.LocalCachedPath
	}
	if localPath == "" {
		http.Error(w, "file has no local source path", http.StatusNotFound)
		return
	}

	info, err := os.Stat(localPath)
	if err != nil {
		http.Error(w, "source file missing", http.StatusNotFound)
		return
	}

	maxBytes := m.maxFileBytes()
	if info.IsDir() || entry.IsDir {
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("X-Is-Dir", "true")
		w.Header().Set("X-File-Name", filepath.Base(localPath))
		_ = StreamDirAsTar(w, localPath, maxBytes)
		return
	}

	if maxBytes > 0 && info.Size() > maxBytes {
		http.Error(w, "file exceeds max transfer limit", http.StatusRequestEntityTooLarge)
		return
	}
	f, err := os.Open(localPath)
	if err != nil {
		http.Error(w, "cannot open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Is-Dir", "false")
	w.Header().Set("X-File-Name", filepath.Base(localPath))
	w.Header().Add("Trailer", "X-File-SHA256")

	h := sha256.New()
	_, _ = io.Copy(io.MultiWriter(w, h), io.LimitReader(f, info.Size()))
	w.Header().Set("X-File-SHA256", hex.EncodeToString(h.Sum(nil)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func extractPeerHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return ""
	}
	return host
}

func extractPeerListenAddr(remoteAddr string, port int) string {
	host := extractPeerHost(remoteAddr)
	if host == "" || port <= 0 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func fmtURL(addr, path string) string {
	return fmt.Sprintf("https://%s%s", addr, path)
}
