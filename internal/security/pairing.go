package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"unionclip/internal/model"
)

var (
	ErrNoActivePairing = errors.New("no active pairing session")
	ErrPairingExpired  = errors.New("pairing PIN expired")
	ErrTooManyAttempts = errors.New("too many invalid PIN attempts")
	ErrInvalidPIN      = errors.New("invalid PIN or proof mismatch")
)

const (
	PairingTTL         = 3 * time.Minute
	MaxPairingAttempts = 5
)

type PairInitRequest struct {
	ClientDeviceID  string `json:"client_device_id"`
	ClientName      string `json:"client_name"`
	ClientOS        string `json:"client_os"`
	ClientNonce     string `json:"client_nonce"`
	ClientPort      int    `json:"client_port"`
	ClientCertFP    string `json:"-"` // Populated from TLS PeerCertificates on server
}

type PairInitResponse struct {
	SessionID   string           `json:"session_id"`
	ServerNonce string           `json:"server_nonce"`
	ServerInfo  model.DeviceInfo `json:"server_info"`
}

type PairConfirmRequest struct {
	SessionID   string `json:"session_id"`
	ClientProof string `json:"client_proof"`
	ClientAddr  string `json:"-"`
	ClientFP    string `json:"-"` // Populated from TLS PeerCertificates on server
}

type PairConfirmResponse struct {
	ServerProof string           `json:"server_proof"`
	ServerInfo  model.DeviceInfo `json:"server_info"`
}

type pendingChallenge struct {
	sessionID    string
	clientID     string
	clientName   string
	clientOS     string
	clientPort   int
	clientFP     string
	clientNonce  string
	serverNonce  string
	createdAt    time.Time
}

type PairingManager struct {
	mu         sync.Mutex
	serverInfo model.DeviceInfo
	pin        string
	expiresAt  time.Time
	attempts   int
	challenges map[string]*pendingChallenge
}

func NewPairingManager(serverInfo model.DeviceInfo) *PairingManager {
	return &PairingManager{
		serverInfo: serverInfo,
		challenges: make(map[string]*pendingChallenge),
	}
}

func (m *PairingManager) StartSession() (pin string, expiresAtMs int64, err error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", 0, err
	}
	pin = fmt.Sprintf("%06d", n.Int64())

	m.mu.Lock()
	defer m.mu.Unlock()
	m.pin = pin
	m.expiresAt = time.Now().Add(PairingTTL)
	m.attempts = 0
	m.challenges = make(map[string]*pendingChallenge)
	return pin, m.expiresAt.UnixMilli(), nil
}

func (m *PairingManager) ActiveStatus() (active bool, expiresAtMs int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pin == "" || time.Now().After(m.expiresAt) || m.attempts >= MaxPairingAttempts {
		return false, 0
	}
	return true, m.expiresAt.UnixMilli()
}

func (m *PairingManager) InitChallenge(req PairInitRequest) (PairInitResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pin == "" {
		return PairInitResponse{}, ErrNoActivePairing
	}
	if time.Now().After(m.expiresAt) {
		m.pin = ""
		return PairInitResponse{}, ErrPairingExpired
	}
	if m.attempts >= MaxPairingAttempts {
		m.pin = ""
		return PairInitResponse{}, ErrTooManyAttempts
	}
	if req.ClientCertFP == "" || req.ClientNonce == "" || req.ClientDeviceID == "" {
		return PairInitResponse{}, errors.New("missing pairing client fields")
	}

	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return PairInitResponse{}, err
	}
	serverNonce := hex.EncodeToString(nonceBytes)
	sid := uuid.NewString()

	m.challenges[sid] = &pendingChallenge{
		sessionID:   sid,
		clientID:    req.ClientDeviceID,
		clientName:  req.ClientName,
		clientOS:    req.ClientOS,
		clientPort:  req.ClientPort,
		clientFP:    req.ClientCertFP,
		clientNonce: req.ClientNonce,
		serverNonce: serverNonce,
		createdAt:   time.Now(),
	}

	return PairInitResponse{
		SessionID:   sid,
		ServerNonce: serverNonce,
		ServerInfo:  m.serverInfo,
	}, nil
}

func (m *PairingManager) ConfirmChallenge(req PairConfirmRequest) (PairConfirmResponse, model.Peer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pin == "" {
		return PairConfirmResponse{}, model.Peer{}, ErrNoActivePairing
	}
	if time.Now().After(m.expiresAt) {
		m.pin = ""
		return PairConfirmResponse{}, model.Peer{}, ErrPairingExpired
	}
	if m.attempts >= MaxPairingAttempts {
		m.pin = ""
		return PairConfirmResponse{}, model.Peer{}, ErrTooManyAttempts
	}

	ch, ok := m.challenges[req.SessionID]
	if !ok {
		return PairConfirmResponse{}, model.Peer{}, errors.New("unknown pairing session")
	}
	// Ensure the same TLS client certificate finishes the handshake
	if ch.clientFP != req.ClientFP {
		return PairConfirmResponse{}, model.Peer{}, errors.New("client certificate mismatch between init and confirm")
	}

	expected := ComputeClientProof(m.pin, m.serverInfo.CertFingerprint, ch.clientFP, ch.serverNonce, ch.clientNonce)
	gotBytes, err1 := hex.DecodeString(req.ClientProof)
	expBytes, err2 := hex.DecodeString(expected)
	if err1 != nil || err2 != nil || !hmac.Equal(gotBytes, expBytes) {
		m.attempts++
		if m.attempts >= MaxPairingAttempts {
			m.pin = ""
			m.challenges = make(map[string]*pendingChallenge)
			return PairConfirmResponse{}, model.Peer{}, ErrTooManyAttempts
		}
		return PairConfirmResponse{}, model.Peer{}, ErrInvalidPIN
	}

	serverProof := ComputeServerProof(m.pin, m.serverInfo.CertFingerprint, ch.clientFP, ch.serverNonce, ch.clientNonce)

	// Single-use PIN: clear on success
	m.pin = ""
	m.challenges = make(map[string]*pendingChallenge)

	now := time.Now().UnixMilli()
	lastAddr := req.ClientAddr
	if lastAddr != "" {
		if _, _, splitErr := net.SplitHostPort(lastAddr); splitErr != nil {
			port := ch.clientPort
			if port <= 0 {
				port = 47831
			}
			lastAddr = net.JoinHostPort(lastAddr, strconv.Itoa(port))
		}
	}
	peer := model.Peer{
		DeviceID:        ch.clientID,
		DeviceName:      ch.clientName,
		OS:              ch.clientOS,
		CertFingerprint: ch.clientFP,
		LastAddr:        lastAddr,
		PairedAt:        now,
		LastSeenAt:      now,
	}
	return PairConfirmResponse{
		ServerProof: serverProof,
		ServerInfo:  m.serverInfo,
	}, peer, nil
}

func RandomNonceHex() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func pairingTranscript(serverFP, clientFP, serverNonce, clientNonce string) []byte {
	return []byte("unionclip-pair-v1|" + serverFP + "|" + clientFP + "|" + serverNonce + "|" + clientNonce)
}

func ComputeClientProof(pin, serverFP, clientFP, serverNonce, clientNonce string) string {
	key := sha256.Sum256([]byte(pin + ":unionclip-client"))
	mac := hmac.New(sha256.New, key[:])
	mac.Write(pairingTranscript(serverFP, clientFP, serverNonce, clientNonce))
	return hex.EncodeToString(mac.Sum(nil))
}

func ComputeServerProof(pin, serverFP, clientFP, serverNonce, clientNonce string) string {
	key := sha256.Sum256([]byte(pin + ":unionclip-server"))
	mac := hmac.New(sha256.New, key[:])
	mac.Write(pairingTranscript(serverFP, clientFP, serverNonce, clientNonce))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyServerProof(pin, serverFP, clientFP, serverNonce, clientNonce, gotProof string) bool {
	expected := ComputeServerProof(pin, serverFP, clientFP, serverNonce, clientNonce)
	gotB, err1 := hex.DecodeString(gotProof)
	expB, err2 := hex.DecodeString(expected)
	return err1 == nil && err2 == nil && hmac.Equal(gotB, expB)
}
