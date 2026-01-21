package crypto

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

// GenerateKeyPair generates a new Ed25519 key pair
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// SavePrivateKey saves an Ed25519 private key to a PEM file
func SavePrivateKey(privKey ed25519.PrivateKey, path string) error {
	// PKCS8 encoding for Ed25519 private key
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}

	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8,
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	return pem.Encode(f, block)
}

// SavePublicKey saves an Ed25519 public key to a PEM file
func SavePublicKey(pubKey ed25519.PublicKey, path string) error {
	pkix, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}

	block := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pkix,
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	return pem.Encode(f, block)
}

// LoadPrivateKey loads an Ed25519 private key from a PEM file
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	privKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an Ed25519 private key")
	}

	return privKey, nil
}

// LoadPublicKey loads an Ed25519 public key from a PEM file
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	pubKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an Ed25519 public key")
	}

	return pubKey, nil
}

// GenerateTLSCertificate creates a self-signed TLS certificate using the Ed25519 key
func GenerateTLSCertificate(privKey ed25519.PrivateKey) (tls.Certificate, error) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Slipstream DNS Tunnel"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	pubKey := privKey.Public().(ed25519.PublicKey)
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, pubKey, privKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  privKey,
		Leaf:        &template,
	}, nil
}

// PublicKeyFingerprint returns the SHA256 fingerprint of a public key in base64
func PublicKeyFingerprint(pubKey ed25519.PublicKey) string {
	hash := sha256.Sum256(pubKey)
	return base64.StdEncoding.EncodeToString(hash[:])
}

// CreatePinningVerifier creates a TLS verification callback that pins to a specific public key fingerprint
func CreatePinningVerifier(expectedFingerprint string) func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("no certificates provided")
		}

		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("parse certificate: %w", err)
		}

		pubKey, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok {
			return errors.New("certificate does not contain Ed25519 public key")
		}

		fingerprint := PublicKeyFingerprint(pubKey)
		if fingerprint != expectedFingerprint {
			return fmt.Errorf("certificate fingerprint mismatch: got %s, expected %s", fingerprint, expectedFingerprint)
		}

		return nil
	}
}

// GetTLSConfig returns a TLS config for the server using the given private key
func GetTLSConfig(privKey ed25519.PrivateKey) (*tls.Config, error) {
	cert, err := GenerateTLSCertificate(privKey)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"slipstream"},
	}, nil
}

// GetClientTLSConfig returns a TLS config for the client with certificate pinning
func GetClientTLSConfig(expectedFingerprint string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify:    true, // Skip default verification
		VerifyPeerCertificate: CreatePinningVerifier(expectedFingerprint),
		NextProtos:            []string{"slipstream"},
	}
}

// SignerFromPrivateKey returns a crypto.Signer from an Ed25519 private key
func SignerFromPrivateKey(privKey ed25519.PrivateKey) crypto.Signer {
	return privKey
}
