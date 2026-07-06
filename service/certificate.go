package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

var ErrCertificateKeyMismatch = errors.New("certificate and key do not match")

// GenerateSelfSignedCertificate creates a self-signed certificate/key pair for the given
// domain (which may also be an IP address) and writes them as PEM files to certPath/keyPath.
func GenerateSelfSignedCertificate(domain string, certPath string, keyPath string) error {
	if domain == "" {
		return errors.New("domain must not be empty")
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   domain,
			Organization: []string{"CasaOS self-signed"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{domain}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("failed to write certificate file: %w", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	return nil
}

// ValidateAndSaveCertificate checks that certPEM/keyPEM form a valid pair, then writes them to
// certPath/keyPath. It returns the certificate's primary CommonName (or SAN) for display purposes.
func ValidateAndSaveCertificate(certPEM []byte, keyPEM []byte, certPath string, keyPath string) (string, error) {
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrCertificateKeyMismatch, err.Error())
	}

	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("failed to parse uploaded certificate: %w", err)
	}

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", fmt.Errorf("failed to write certificate file: %w", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", fmt.Errorf("failed to write key file: %w", err)
	}

	name := leaf.Subject.CommonName
	if name == "" && len(leaf.DNSNames) > 0 {
		name = leaf.DNSNames[0]
	}
	if name == "" && len(leaf.IPAddresses) > 0 {
		name = leaf.IPAddresses[0].String()
	}

	return name, nil
}
