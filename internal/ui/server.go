package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"unionclip/internal/model"
)

//go:embed web/*
var webFS embed.FS

type UIState struct {
	Device               model.DeviceInfo         `json:"device"`
	Config               model.AppConfig          `json:"config"`
	Peers                []model.Peer             `json:"peers"`
	Discovered           []model.DiscoveredDevice `json:"discovered"`
	AccessibilityGranted bool                     `json:"accessibility_granted"`
	CacheBytes           int64                    `json:"cache_bytes"`
	OS                   string                   `json:"os"`
}

type Backend interface {
	UIState() UIState
	ListHistory(filter model.HistoryFilter) ([]model.ClipboardItem, error)
	SelectHistoryItem(id string, paste bool) error
	UpdateHistoryText(id, text string) (model.ClipboardItem, error)
	DeleteHistoryItem(id string) error
	ClearHistory() error
	SaveItemAsTemplate(itemID, folderID, title string) (model.TemplateItem, error)
	ListTemplates() ([]model.TemplateFolder, []model.TemplateItem, error)
	UpsertTemplateFolder(f model.TemplateFolder) (model.TemplateFolder, error)
	DeleteTemplateFolder(id string) error
	UpsertTemplate(t model.TemplateItem) (model.TemplateItem, error)
	DeleteTemplate(id string) error
	SelectTemplate(id string, paste bool) error
	StartPairing() (string, int64, error)
	ConnectWithPIN(addr, pin string) (model.Peer, error)
	UpdatePeerAddr(id, addr string) error
	RemovePeer(id string) error
	UpdateConfig(cfg model.AppConfig) (model.AppConfig, error)
	ClearCache() error
	RequestAccessibility()
	RevealItemFiles(id string) error
	ReadBlob(sha256Hex string) ([]byte, error)
	ReadThumb(sha256Hex string) ([]byte, error)
}

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	backend Backend
	token   string
	port    int
	srv     *http.Server

	mu    sync.Mutex
	conns map[*websocket.Conn]*sync.Mutex
}

func NewServer(backend Backend) (*Server, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return &Server{
		backend: backend,
		token:   hex.EncodeToString(b),
		conns:   make(map[*websocket.Conn]*sync.Mutex),
	}, nil
}

func (s *Server) Start(ctx context.Context) (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.srv = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = s.srv.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		_ = s.srv.Close()
	}()
	return s.port, nil
}

func (s *Server) ViewerURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/?token=%s", s.port, s.token)
}

func (s *Server) BroadcastEvent(event string, payload any) {
	msg := map[string]any{
		"event":   event,
		"payload": payload,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.Lock()
	snapshot := make([]struct {
		c  *websocket.Conn
		mu *sync.Mutex
	}, 0, len(s.conns))
	for c, m := range s.conns {
		snapshot = append(snapshot, struct {
			c  *websocket.Conn
			mu *sync.Mutex
		}{c, m})
	}
	s.mu.Unlock()

	for _, item := range snapshot {
		item.mu.Lock()
		_ = item.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := item.c.WriteMessage(websocket.TextMessage, b)
		item.mu.Unlock()
		if err != nil {
			s.removeWS(item.c)
		}
	}
}

func (s *Server) removeWS(c *websocket.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

func (s *Server) validToken(candidate string) bool {
	if len(candidate) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

func (s *Server) isAuthed(r *http.Request) bool {
	if c, err := r.Cookie("uc_session"); err == nil && s.validToken(c.Value) {
		return true
	}
	if h := r.Header.Get("X-Session-Token"); h != "" && s.validToken(h) {
		return true
	}
	if q := r.URL.Query().Get("token"); q != "" && s.validToken(q) {
		return true
	}
	return false
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) routes() http.Handler {
	subFS, _ := fs.Sub(webFS, "web")
	fileSrv := http.FileServer(http.FS(subFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if tok := r.URL.Query().Get("token"); tok != "" && s.validToken(tok) {
				http.SetCookie(w, &http.Cookie{
					Name:     "uc_session",
					Value:    s.token,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
				})
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			if !s.isAuthed(r) {
				http.Error(w, "unauthorized viewer session", http.StatusUnauthorized)
				return
			}
		}
		fileSrv.ServeHTTP(w, r)
	})

	mux.HandleFunc("/ws", s.requireAuth(s.handleWS))
	mux.HandleFunc("/api/state", s.requireAuth(s.handleState))
	mux.HandleFunc("/api/history", s.requireAuth(s.handleHistory))
	mux.HandleFunc("/api/history/clear", s.requireAuth(s.handleHistoryClear))
	mux.HandleFunc("/api/history/", s.requireAuth(s.handleHistoryItem))
	mux.HandleFunc("/api/blobs/", s.requireAuth(s.handleBlob))
	mux.HandleFunc("/api/thumbs/", s.requireAuth(s.handleThumb))
	mux.HandleFunc("/api/templates", s.requireAuth(s.handleTemplates))
	mux.HandleFunc("/api/templates/folders", s.requireAuth(s.handleTemplateFolders))
	mux.HandleFunc("/api/templates/folders/", s.requireAuth(s.handleTemplateFolderItem))
	mux.HandleFunc("/api/templates/", s.requireAuth(s.handleTemplateItem))
	mux.HandleFunc("/api/pairing/start", s.requireAuth(s.handlePairingStart))
	mux.HandleFunc("/api/pairing/connect", s.requireAuth(s.handlePairingConnect))
	mux.HandleFunc("/api/peers/", s.requireAuth(s.handlePeerItem))
	mux.HandleFunc("/api/config", s.requireAuth(s.handleConfig))
	mux.HandleFunc("/api/cache/clear", s.requireAuth(s.handleCacheClear))
	mux.HandleFunc("/api/system/accessibility", s.requireAuth(s.handleAccessibility))
	mux.HandleFunc("/api/files/", s.requireAuth(s.handleRevealFiles))
	return mux
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conns[c] = &sync.Mutex{}
	s.mu.Unlock()
	defer s.removeWS(c)

	for {
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.UIState())
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	items, err := s.backend.ListHistory(model.HistoryFilter{
		Search:         q.Get("search"),
		Kind:           model.ItemKind(q.Get("kind")),
		OriginDeviceID: q.Get("device"),
		OriginOS:       q.Get("os"),
		Limit:          200,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.backend.ClearHistory(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleHistoryItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/history/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "select" && r.Method == http.MethodPost {
		var body struct {
			Paste bool `json:"paste"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.backend.SelectHistoryItem(id, body.Paste); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "to-template" && r.Method == http.MethodPost {
		var body struct {
			FolderID string `json:"folder_id"`
			Title    string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		t, err := s.backend.SaveItemAsTemplate(id, body.FolderID, body.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, t)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var body struct {
			TextContent string `json:"text_content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		it, err := s.backend.UpdateHistoryText(id, body.TextContent)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, it)
	case http.MethodDelete:
		if err := s.backend.DeleteHistoryItem(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	sha := strings.TrimPrefix(r.URL.Path, "/api/blobs/")
	b, err := s.backend.ReadBlob(sha)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(b)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	sha := strings.TrimPrefix(r.URL.Path, "/api/thumbs/")
	b, err := s.backend.ReadThumb(sha)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(b)
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		folders, items, err := s.backend.ListTemplates()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"folders": folders,
			"items":   items,
		})
	case http.MethodPost:
		var t model.TemplateItem
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.backend.UpsertTemplate(t)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTemplateFolders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var f model.TemplateFolder
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	saved, err := s.backend.UpsertTemplateFolder(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleTemplateFolderItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/templates/folders/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var f model.TemplateFolder
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		f.ID = id
		saved, err := s.backend.UpsertTemplateFolder(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if err := s.backend.DeleteTemplateFolder(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTemplateItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/templates/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "select" && r.Method == http.MethodPost {
		var body struct {
			Paste bool `json:"paste"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.backend.SelectTemplate(id, body.Paste); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	switch r.Method {
	case http.MethodPut:
		var t model.TemplateItem
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		t.ID = id
		saved, err := s.backend.UpsertTemplate(t)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if err := s.backend.DeleteTemplate(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePairingStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pin, exp, err := s.backend.StartPairing()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pin":        pin,
		"expires_at": exp,
	})
}

func (s *Server) handlePairingConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Addr string `json:"addr"`
		PIN  string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	peer, err := s.backend.ConnectWithPIN(body.Addr, body.PIN)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, peer)
}

func (s *Server) handlePeerItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/peers/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		http.Error(w, "missing peer id", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "addr" && r.Method == http.MethodPut {
		var body struct {
			ManualAddr string `json:"manual_addr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := s.backend.UpdatePeerAddr(id, body.ManualAddr); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.backend.RemovePeer(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cfg model.AppConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	saved, err := s.backend.UpdateConfig(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.backend.ClearCache(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAccessibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.backend.RequestAccessibility()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRevealFiles(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/files/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "reveal" && r.Method == http.MethodPost {
		if err := s.backend.RevealItemFiles(parts[0]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
