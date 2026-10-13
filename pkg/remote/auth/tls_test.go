package auth

import (
	"crypto/x509"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/peer"
)

func TestR9_CertificateIsMadeOncePerDataDir(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	c, err := s.EnsureCertificate([]string{"192.168.1.5", "deck.local"})
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`), c.Fingerprint)
	require.Len(t, c.Hex, 32*3-1)
	leaf, err := x509.ParseCertificate(c.TLS.Certificate[0])
	require.NoError(t, err)
	require.Equal(t, x509.ECDSA, leaf.PublicKeyAlgorithm)
	require.NoError(t, leaf.VerifyHostname("192.168.1.5"))
	require.NoError(t, leaf.VerifyHostname("deck.local"))
	require.NoError(t, leaf.VerifyHostname("127.0.0.1"))
	require.WithinDuration(t, clk.now().Add(certLife), leaf.NotAfter, time.Minute)
	require.NoError(t, peer.CheckPrivate(s.path(KeyFile)))

	again, err := s.EnsureCertificate([]string{"192.168.1.5"})
	require.NoError(t, err)
	require.Equal(t, c.Fingerprint, again.Fingerprint, "the same certificate is served again")

	moved, err := s.EnsureCertificate([]string{"10.1.1.1"})
	require.NoError(t, err)
	require.NotEqual(t, c.Fingerprint, moved.Fingerprint, "a new address needs a new certificate")

	clk.advance(certLife - renewBefore + time.Hour)
	renewed, err := s.EnsureCertificate([]string{"10.1.1.1"})
	require.NoError(t, err)
	require.NotEqual(t, moved.Fingerprint, renewed.Fingerprint, "an expiring certificate is renewed")
}
