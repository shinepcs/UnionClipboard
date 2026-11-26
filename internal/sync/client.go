package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// PairWithRemote performs channel-bound 6-digit PIN pairing over TLS 1.3 and saves the peer.
func (m *Manager) PairWithRemote(ctx context.Context, addr, pin string) (model.Peer, error) {
	addr = strings.TrimSpace(addr)
	pin = strings.TrimSpace(pin)
	if addr == "" || len(pin) != 6 {
		return model.Peer{}, errors.New("address and 6-digit PIN are required")
	}
	if !strings.Contains(addr, ":") {
		addr = fmt.Sprintf("%s:%d", addr, 47831)
	}

	cNonce, err := security.RandomNonceHex()
	if err != nil {
		return model.Peer{}, err
	}

	initClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: security.PairingClientTLSConfig(m.cert),
		},
	}

	initReq := security.PairInitRequest{
		ClientDeviceID: m.info.DeviceID,
		ClientName:     m.info.DeviceName,
		ClientOS:       m.info.OS,
		ClientNonce:    cNonce,
		ClientPort:     m.ListenPort(),
	}
	body, _ := json.Marshal(initReq)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmtURL(addr, "/pair/init"), bytes.NewReader(body))
	if err != nil {
		return model.Peer{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := initClient.Do(req)
	if err != nil {
		return model.Peer{}, fmt.Errorf("connect %s: %w", addr, err)
	}
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return model.Peer{}, fmt.Errorf("pairing init failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return model.Peer{}, errors.New("server presented no TLS certificate")
	}
	serverFP := security.CertFingerprint(resp.TLS.PeerCertificates[0])

	var initResp security.PairInitResponse
	if err := json.Unmarshal(respBytes, &initResp); err != nil {
		return model.Peer{}, err
	}
	if initResp.ServerInfo.CertFingerprint != "" && initResp.ServerInfo.CertFingerprint != serverFP {
		return model.Peer{}, errors.New("server certificate fingerprint mismatch")
	}

	clientProof := security.ComputeClientProof(pin, serverFP, m.fp, initResp.ServerNonce, cNonce)

	pinnedClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: security.ClientTLSConfig(m.cert, serverFP),
		},
	}
	confReq := security.PairConfirmRequest{
		SessionID:   initResp.SessionID,
		ClientProof: clientProof,
	}
	confBody, _ := json.Marshal(confReq)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, fmtURL(addr, "/pair/confirm"), bytes.NewReader(confBody))
	if err != nil {
		return model.Peer{}, err
	}
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := pinnedClient.Do(req2)
	if err != nil {
		return model.Peer{}, err
	}
	resp2Bytes, _ := io.ReadAll(io.LimitReader(resp2.Body, 64*1024))
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return model.Peer{}, fmt.Errorf("pairing confirm failed (%d): %s", resp2.StatusCode, strings.TrimSpace(string(resp2Bytes)))
	}

	var confResp security.PairConfirmResponse
	if err := json.Unmarshal(resp2Bytes, &confResp); err != nil {
		return model.Peer{}, err
	}
	if !security.VerifyServerProof(pin, serverFP, m.fp, initResp.ServerNonce, cNonce, confResp.ServerProof) {
		return model.Peer{}, errors.New("server proof verification failed (possible MITM)")
	}

	now := time.Now().UnixMilli()
	peer := model.Peer{
		DeviceID:        confResp.ServerInfo.DeviceID,
		DeviceName:      confResp.ServerInfo.DeviceName,
		OS:              confResp.ServerInfo.OS,
		CertFingerprint: serverFP,
		LastAddr:        addr,
		ManualAddr:      addr,
		PairedAt:        now,
		LastSeenAt:      now,
	}
	if err := m.store.UpsertPeer(peer); err != nil {
		return model.Peer{}, err
	}
	m.notifyPeersChanged()
	m.TriggerConnect(peer.DeviceID)
	return peer, nil
}

func (m *Manager) newPeerHTTPClient(peer model.Peer, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: security.ClientTLSConfig(m.cert, peer.CertFingerprint),
		},
	}
}

func (m *Manager) dialPeerWS(ctx context.Context, peer model.Peer, addr string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		TLSClientConfig:  security.ClientTLSConfig(m.cert, peer.CertFingerprint),
		HandshakeTimeout: 8 * time.Second,
	}
	wsURL := fmt.Sprintf("wss://%s/api/v1/sync/stream?port=%d", addr, m.ListenPort())
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	return conn, err
}

func (m *Manager) fetchBlobFromPeer(ctx context.Context, peerID, sha256Hex string) error {
	if !store.IsValidSHA256(sha256Hex) || m.store.HasBlob(sha256Hex) {
		return nil
	}
	peer, addr, err := m.resolvePeerAddr(peerID)
	if err != nil {
		return err
	}
	client := m.newPeerHTTPClient(peer, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmtURL(addr, "/api/v1/blobs/"+sha256Hex), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch blob status %d", resp.StatusCode)
	}
	maxImg := m.maxImageBytes()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImg+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxImg {
		return errors.New("remote image exceeds max image size")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sha256Hex {
		return errors.New("blob sha256 mismatch")
	}
	_, err = m.store.SaveBlob(sha256Hex, data)
	return err
}

// FetchRemoteFiles downloads missing remote files on-demand into received_files/<itemID>/
// and updates LocalCachedPath in SQLite, returning local paths ready for OS clipboard.
func (m *Manager) FetchRemoteFiles(ctx context.Context, item model.ClipboardItem, progressCb func(model.FileProgressEvent)) ([]string, error) {
	if item.Kind != model.KindFiles || len(item.Files) == 0 {
		return nil, errors.New("item has no files")
	}
	if !item.NeedsRemoteFetch(m.info.DeviceID) {
		return item.LocalPaths(m.info.DeviceID), nil
	}

	peer, addr, err := m.resolvePeerAddr(item.OriginDeviceID)
	if err != nil {
		return nil, err
	}
	recDir, err := m.store.ReceivedItemDir(item.ID)
	if err != nil {
		return nil, err
	}

	client := m.newPeerHTTPClient(peer, 0) // stream large files without short overall timeout
	maxBytes := m.maxFileBytes()
	updatedFiles := make([]model.FileEntry, len(item.Files))
	copy(updatedFiles, item.Files)

	usedNames := make(map[string]int)
	for i, entry := range updatedFiles {
		if entry.LocalCachedPath != "" {
			if _, statErr := os.Stat(entry.LocalCachedPath); statErr == nil {
				usedNames[filepath.Base(entry.LocalCachedPath)]++
				continue
			}
		}

		safeBase := filepath.Base(filepath.FromSlash(entry.RelativeName))
		if safeBase == "" || safeBase == "." || safeBase == ".." {
			safeBase = fmt.Sprintf("file_%d", i)
		}
		if cnt := usedNames[safeBase]; cnt > 0 {
			ext := filepath.Ext(safeBase)
			stem := strings.TrimSuffix(safeBase, ext)
			safeBase = fmt.Sprintf("%s_%d%s", stem, cnt, ext)
		}
		usedNames[safeBase]++
		destPath := filepath.Join(recDir, safeBase)

		url := fmtURL(addr, fmt.Sprintf("/api/v1/files/%s/%d", item.ID, i))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", entry.RelativeName, err)
		}

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return nil, fmt.Errorf("remote rejected %s (%d): %s", entry.RelativeName, resp.StatusCode, strings.TrimSpace(string(b)))
		}

		isDir := entry.IsDir || resp.Header.Get("X-Is-Dir") == "true"
		if isDir {
			_ = os.RemoveAll(destPath)
			err = ExtractTarSafely(resp.Body, destPath, maxBytes)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("extract folder %s: %w", entry.RelativeName, err)
			}
			if progressCb != nil {
				progressCb(model.FileProgressEvent{
					ItemID:     item.ID,
					FileIndex:  i,
					FileName:   entry.RelativeName,
					BytesDone:  entry.Size,
					BytesTotal: entry.Size,
					Done:       true,
				})
			}
		} else {
			total := entry.Size
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				if parsed, pErr := strconv.ParseInt(cl, 10, 64); pErr == nil && parsed > 0 {
					total = parsed
				}
			}
			partPath := destPath + ".part"
			out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				resp.Body.Close()
				return nil, err
			}

			h := sha256.New()
			var done int64
			buf := make([]byte, 64*1024)
			var copyErr error
			for {
				nr, rErr := resp.Body.Read(buf)
				if nr > 0 {
					done += int64(nr)
					if maxBytes > 0 && done > maxBytes {
						copyErr = fmt.Errorf("file exceeds max allowed download size (%d bytes)", maxBytes)
						break
					}
					if _, wErr := out.Write(buf[:nr]); wErr != nil {
						copyErr = wErr
						break
					}
					_, _ = h.Write(buf[:nr])
					if progressCb != nil {
						progressCb(model.FileProgressEvent{
							ItemID:     item.ID,
							FileIndex:  i,
							FileName:   entry.RelativeName,
							BytesDone:  done,
							BytesTotal: total,
						})
					}
				}
				if rErr == io.EOF {
					break
				}
				if rErr != nil {
					copyErr = rErr
					break
				}
			}
			_ = out.Close()
			trailerHash := resp.Trailer.Get("X-File-SHA256")
			resp.Body.Close()

			if copyErr != nil {
				_ = os.Remove(partPath)
				return nil, copyErr
			}
			gotHash := hex.EncodeToString(h.Sum(nil))
			if trailerHash != "" && trailerHash != gotHash {
				_ = os.Remove(partPath)
				return nil, fmt.Errorf("sha256 mismatch for %s", entry.RelativeName)
			}
			if err := os.Rename(partPath, destPath); err != nil {
				_ = os.Remove(partPath)
				return nil, err
			}
			updatedFiles[i].SHA256 = gotHash
			if progressCb != nil {
				progressCb(model.FileProgressEvent{
					ItemID:     item.ID,
					FileIndex:  i,
					FileName:   entry.RelativeName,
					BytesDone:  done,
					BytesTotal: total,
					Done:       true,
				})
			}
		}

		updatedFiles[i].LocalCachedPath = destPath
	}

	if err := m.store.UpdateItemFiles(item.ID, updatedFiles); err != nil {
		return nil, err
	}
	outPaths := make([]string, len(updatedFiles))
	for i, f := range updatedFiles {
		outPaths[i] = f.LocalCachedPath
	}
	return outPaths, nil
}

// Keep unused tls import check clean
var _ = tls.VersionTLS13
