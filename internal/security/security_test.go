package security

import (
	"crypto/tls"
	"net"
	"testing"

	"unionclip/internal/model"
)

func TestCertCreationAndMTLS(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	certA, fpA, err := LoadOrCreateDeviceCert(dirA, "dev-a")
	if err != nil {
		t.Fatalf("certA: %v", err)
	}
	certA2, fpA2, err := LoadOrCreateDeviceCert(dirA, "dev-a")
	if err != nil || fpA2 != fpA || len(certA2.Certificate) == 0 {
		t.Fatalf("expected persisted cert reload, got %s vs %s (%v)", fpA2, fpA, err)
	}

	certB, fpB, err := LoadOrCreateDeviceCert(dirB, "dev-b")
	if err != nil {
		t.Fatalf("certB: %v", err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", ServerTLSConfig(certA))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				tc := conn.(*tls.Conn)
				_ = tc.Handshake()
			}(c)
		}
	}()

	// Valid pinned fingerprint succeeds
	conn, err := tls.Dial("tcp", ln.Addr().String(), ClientTLSConfig(certB, fpA))
	if err != nil {
		t.Fatalf("expected valid pinned TLS dial, got %v", err)
	}
	conn.Close()

	// Wrong fingerprint fails
	if _, err := tls.Dial("tcp", ln.Addr().String(), ClientTLSConfig(certB, fpB)); err == nil {
		t.Fatal("expected certificate fingerprint mismatch error")
	}
}

func TestPairingChannelBindingAndRateLimit(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	_, fpA, _ := LoadOrCreateDeviceCert(dirA, "dev-a")
	_, fpB, _ := LoadOrCreateDeviceCert(dirB, "dev-b")

	pm := NewPairingManager(model.DeviceInfo{
		DeviceID:        "dev-a",
		DeviceName:      "Mac",
		OS:              "darwin",
		CertFingerprint: fpA,
		Port:            47831,
	})

	pin, _, err := pm.StartSession()
	if err != nil || len(pin) != 6 {
		t.Fatalf("start session: pin=%q err=%v", pin, err)
	}

	cNonce, _ := RandomNonceHex()
	initResp, err := pm.InitChallenge(PairInitRequest{
		ClientDeviceID: "dev-b",
		ClientName:     "Win",
		ClientOS:       "windows",
		ClientNonce:    cNonce,
		ClientCertFP:   fpB,
	})
	if err != nil {
		t.Fatalf("init challenge: %v", err)
	}

	// MITM certificate substitution fails proof check
	mitmProof := ComputeClientProof(pin, "deadbeef", fpB, initResp.ServerNonce, cNonce)
	if _, _, err := pm.ConfirmChallenge(PairConfirmRequest{
		SessionID:   initResp.SessionID,
		ClientProof: mitmProof,
		ClientFP:    fpB,
	}); err != ErrInvalidPIN {
		t.Fatalf("expected ErrInvalidPIN on MITM transcript, got %v", err)
	}

	// Valid proof succeeds
	validProof := ComputeClientProof(pin, fpA, fpB, initResp.ServerNonce, cNonce)
	confResp, peer, err := pm.ConfirmChallenge(PairConfirmRequest{
		SessionID:   initResp.SessionID,
		ClientProof: validProof,
		ClientFP:    fpB,
		ClientAddr:  "127.0.0.1:47831",
	})
	if err != nil {
		t.Fatalf("confirm valid: %v", err)
	}
	if peer.CertFingerprint != fpB {
		t.Fatalf("unexpected peer fp: %s", peer.CertFingerprint)
	}
	if !VerifyServerProof(pin, fpA, fpB, initResp.ServerNonce, cNonce, confResp.ServerProof) {
		t.Fatal("server proof verification failed")
	}

	// Rate limit lockout after 5 bad attempts
	pin2, _, _ := pm.StartSession()
	_ = pin2
	init2, err := pm.InitChallenge(PairInitRequest{
		ClientDeviceID: "dev-b",
		ClientNonce:    cNonce,
		ClientCertFP:   fpB,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_, _, err = pm.ConfirmChallenge(PairConfirmRequest{
			SessionID:   init2.SessionID,
			ClientProof: "0000",
			ClientFP:    fpB,
		})
		if err != ErrInvalidPIN {
			t.Fatalf("attempt %d: expected ErrInvalidPIN, got %v", i, err)
		}
	}
	_, _, err = pm.ConfirmChallenge(PairConfirmRequest{
		SessionID:   init2.SessionID,
		ClientProof: "0000",
		ClientFP:    fpB,
	})
	if err != ErrTooManyAttempts {
		t.Fatalf("expected ErrTooManyAttempts on 5th failure, got %v", err)
	}
}
