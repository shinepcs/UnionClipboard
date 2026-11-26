package clipboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"unionclip/internal/model"
)

func HashText(s string) string {
	sum := sha256.Sum256([]byte("text:" + s))
	return hex.EncodeToString(sum[:])
}

func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func HashFiles(files []model.FileEntry) string {
	type norm struct {
		Name  string `json:"n"`
		Size  int64  `json:"s"`
		IsDir bool   `json:"d"`
		MTime int64  `json:"m"`
	}
	list := make([]norm, len(files))
	for i, f := range files {
		list[i] = norm{
			Name:  f.RelativeName,
			Size:  f.Size,
			IsDir: f.IsDir,
			MTime: f.ModTime,
		}
	}
	b, _ := json.Marshal(list)
	sum := sha256.Sum256(append([]byte("files:"), b...))
	return hex.EncodeToString(sum[:])
}

func BuildTitle(kind model.ItemKind, text string, w, h int, files []model.FileEntry) string {
	switch kind {
	case model.KindImage:
		if w > 0 && h > 0 {
			return fmt.Sprintf("이미지 (%dx%d)", w, h)
		}
		return "이미지"
	case model.KindFiles:
		if len(files) == 0 {
			return "파일"
		}
		if len(files) == 1 {
			if files[0].IsDir {
				return fmt.Sprintf("[폴더] %s", files[0].RelativeName)
			}
			return files[0].RelativeName
		}
		return fmt.Sprintf("%s 외 %d개", files[0].RelativeName, len(files)-1)
	default:
		clean := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", " "), "\n", " "))
		clean = strings.Join(strings.Fields(clean), " ")
		if clean == "" {
			return "(공백 텍스트)"
		}
		return TruncateRunes(clean, 56)
	}
}

func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= maxRunes-1 {
			b.WriteRune('…')
			break
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}

// LoopSuppressor prevents re-capturing clipboard content that UnionClipboard just wrote itself.
type LoopSuppressor struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]time.Time
}

func NewLoopSuppressor(ttl time.Duration) *LoopSuppressor {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &LoopSuppressor{
		ttl:     ttl,
		entries: make(map[string]time.Time),
	}
}

func (l *LoopSuppressor) Suppress(contentHash string) {
	if contentHash == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.gcLocked(now)
	l.entries[contentHash] = now.Add(l.ttl)
}

func (l *LoopSuppressor) IsSuppressed(contentHash string) bool {
	if contentHash == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.gcLocked(now)
	exp, ok := l.entries[contentHash]
	return ok && now.Before(exp)
}

func (l *LoopSuppressor) gcLocked(now time.Time) {
	for k, exp := range l.entries {
		if !now.Before(exp) {
			delete(l.entries, k)
		}
	}
}
