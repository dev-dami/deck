package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
)

// Certificate is the LAN certificate of R9: self-signed, ECDSA P-256, one
// year, its SANs the LAN addresses, kept in tls.{crt,key} under the store.
type Certificate struct {
	TLS tls.Certificate
	// Fingerprint is "SHA256:" + base64 of sha256 over the DER
	// certificate, the form peer invites carry (fp=).
	Fingerprint string
	// Hex is the same digest as browsers show it: upper-case hex pairs
	// joined by colons.
	Hex string
	// Hosts are the SANs.
	Hosts []string
}

// certLife is how long a certificate is issued for; one that has less
// than renewBefore left, or lacks a host, is made again.
const (
	certLife    = 365 * 24 * time.Hour
	renewBefore = 30 * 24 * time.Hour
)

// EnsureCertificate loads the store's certificate, making a new one when
// there is none, it is about to expire, or it does not name every host.
func (s *Store) EnsureCertificate(hosts []string) (Certificate, error) {
	hosts = cleanHosts(hosts)
	if c, err := s.loadCertificate(); err == nil && c.covers(hosts, s.now()) {
		return c, nil
	}
	return s.makeCertificate(hosts)
}

func cleanHosts(hosts []string) []string {
	seen := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	out := []string{"localhost", "127.0.0.1", "::1"}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

func (c Certificate) covers(hosts []string, now time.Time) bool {
	if len(c.TLS.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(c.TLS.Certificate[0])
	if err != nil || now.After(leaf.NotAfter.Add(-renewBefore)) || now.Before(leaf.NotBefore) {
		return false
	}
	for _, h := range hosts {
		if leaf.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

func (s *Store) loadCertificate() (Certificate, error) {
	cert, err := tls.LoadX509KeyPair(s.path(CertFile), s.path(KeyFile))
	if err != nil {
		return Certificate{}, err
	}
	return describe(cert)
}

func describe(cert tls.Certificate) (Certificate, error) {
	if len(cert.Certificate) == 0 {
		return Certificate{}, errors.New("empty certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return Certificate{}, err
	}
	sum := sha256.Sum256(cert.Certificate[0])
	hexPairs := make([]string, len(sum))
	for i, b := range sum {
		hexPairs[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	hosts := append([]string(nil), leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		hosts = append(hosts, ip.String())
	}
	return Certificate{
		TLS:         cert,
		Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]),
		Hex:         strings.Join(hexPairs, ":"),
		Hosts:       hosts,
	}, nil
}

func (s *Store) makeCertificate(hosts []string) (Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return Certificate{}, err
	}
	now := s.now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Deck remote control"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLife),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	err = s.withLock(func() error {
		if err := peer.WritePrivateFile(s.path(KeyFile), keyPEM); err != nil {
			return err
		}
		return peer.WritePrivateFile(s.path(CertFile), certPEM)
	})
	if err != nil {
		return Certificate{}, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Certificate{}, err
	}
	return describe(cert)
}
