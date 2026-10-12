package auth

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Advert is server.json: where the running server of this data dir
// listens, so `deck remote pair` in another process can print the QR and
// the invite. It is removed when the server stops.
type Advert struct {
	// URL is the origin a phone should open: the first LAN address with
	// --lan, else the loopback one.
	URL string `json:"url"`
	// Hosts are the addresses the server answers by, best first.
	Hosts []string `json:"hosts"`
	Port  int      `json:"port"`
	TLS   bool     `json:"tls"`
	// Fingerprint is the LAN certificate's, as the invite carries it; ""
	// without TLS.
	Fingerprint string    `json:"fingerprint,omitempty"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
}

// ErrNoServer is returned by ReadAdvert when no server is running here.
var ErrNoServer = errors.New("deck remote is not running for this project")

// WriteAdvert records the running server.
func (s *Store) WriteAdvert(a Advert) error {
	return s.withLock(func() error { return s.write(ServerFile, a) })
}

// ClearAdvert removes server.json when it is this process's.
func (s *Store) ClearAdvert() error {
	return s.withLock(func() error {
		a, err := s.ReadAdvert()
		if err != nil {
			return nil
		}
		if a.PID != os.Getpid() {
			return nil
		}
		err = os.Remove(s.path(ServerFile))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

// ReadAdvert returns the running server's advert, or ErrNoServer.
func (s *Store) ReadAdvert() (Advert, error) {
	data, err := os.ReadFile(s.path(ServerFile))
	if errors.Is(err, os.ErrNotExist) {
		return Advert{}, ErrNoServer
	}
	if err != nil {
		return Advert{}, err
	}
	var a Advert
	if err := json.Unmarshal(data, &a); err != nil {
		return Advert{}, err
	}
	return a, nil
}
