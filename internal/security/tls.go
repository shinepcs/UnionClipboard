package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func LeafFingerprint(cert tls.Certificate) (string, error) {
	if cert.Leaf != nil {
		return CertFingerprint(cert.Leaf), nil
	}
	if len(cert.Certificate) == 0 {
		return "", errors.New("empty certificate")
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", err
	}
	return CertFingerprint(parsed), nil
}

// LoadOrCreateDeviceCert loads certs/device.crt & device.key or generates an ECDSA P-256 identity.
func LoadOrCreateDeviceCert(certsDir, deviceID string) (tls.Certificate, string, error) {
	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	certPath := filepath.Join(certsDir, "device.crt")
	keyPath := filepath.Join(certsDir, "device.key")

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil && len(cert.Certificate) > 0 {
				leaf, err := x509.ParseCertificate(cert.Certificate[0])
				if err == nil {
					cert.Leaf = leaf
					return cert, CertFingerprint(leaf), nil
				}
			}
		}
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("generate ecdsa key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   deviceID,
			Organization: []string{"UnionClipboard"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create certificate: %w", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("marshal key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", err
	}
	cert.Leaf = leaf
	return cert, CertFingerprint(leaf), nil
}

// ServerTLSConfig creates a TLS 1.3 config that requests client certificates.
// Individual routes enforce pinned peer identity for /api/v1/* vs channel-bound pairing for /pair/*.
func ServerTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
	}
}

// ClientTLSConfig creates a TLS 1.3 mTLS client config pinned to expectedPeerFP.
func ClientTLSConfig(cert tls.Certificate, expectedPeerFP string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // Verified via VerifyPeerCertificate fingerprint pinning
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("peer presented no certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			if CertFingerprint(leaf) != expectedPeerFP {
				return fmt.Errorf("certificate fingerprint mismatch")
			}
			return nil
		},
	}
}

// PairingClientTLSConfig creates a TLS 1.3 client config for initial PIN pairing.
// Server identity is verified via channel-bound HMAC using ConnectionState().PeerCertificates[0].
func PairingClientTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}
}
