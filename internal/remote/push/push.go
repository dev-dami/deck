// Package push sends Web Push notifications to paired phones.
//
// A notification travels from Deck through the browser vendor's push service
// (Google, Mozilla or Apple) to the phone. The body is encrypted for the phone
// (RFC 8291), so the push service sees only the phone's endpoint and that a
// message was sent. Because that still involves a third party, a phone must
// opt in, and nothing is sent to phones that have not.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// subscriber is the VAPID contact the push services see.
const subscriber = "https://github.com/dev-dami/deck"

const (
	sendTimeout = 15 * time.Second
	maxSubs     = 64
)

// Subscription is one phone's registration.
type Subscription struct {
	Endpoint  string `json:"endpoint"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	Approvals bool   `json:"approvals"`
	Done      bool   `json:"done"`
}

// Message is the JSON the service worker receives.
type Message struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tag       string `json:"tag"`
	SessionID string `json:"session_id,omitempty"`
	Approval  string `json:"approval_id,omitempty"`
}

// Sender delivers one encrypted message; the real one speaks to a push
// service, tests use a fake.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte, urgent bool) (status int, err error)
}

// allowedHosts are the push services browsers use. A paired phone supplies its
// endpoint, so without this a stolen key could aim Deck's requests at any host.
var allowedHosts = []string{
	"fcm.googleapis.com",
	"updates.push.services.mozilla.com",
	"push.services.mozilla.com",
	"web.push.apple.com",
	"push.apple.com",
	"notify.windows.com",
}

// ValidEndpoint reports whether endpoint is an https URL on a known push service.
func ValidEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("push endpoint must be an https URL")
	}
	host := strings.ToLower(u.Hostname())
	for _, ok := range allowedHosts {
		if host == ok || strings.HasSuffix(host, "."+ok) {
			return nil
		}
	}
	return fmt.Errorf("push endpoint host %q is not a known push service", host)
}

type vapid struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// Service keeps VAPID keys and subscriptions under a directory.
type Service struct {
	dir    string
	sender Sender
	keys   vapid

	mu   sync.Mutex
	subs map[string]Subscription // by device id
}

// New loads (or creates) the VAPID keys and subscriptions in dir. A nil sender
// sends for real.
func New(dir string, sender Sender) (*Service, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Service{dir: dir, subs: map[string]Subscription{}}
	if err := s.loadKeys(); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "push.json"), &s.subs); err != nil {
		return nil, err
	}
	if sender == nil {
		sender = &webSender{keys: s.keys, client: &http.Client{Timeout: sendTimeout}}
	}
	s.sender = sender
	return s, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "push.*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o600)); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func (s *Service) loadKeys() error {
	path := filepath.Join(s.dir, "vapid.json")
	if err := readJSON(path, &s.keys); err != nil {
		return err
	}
	if s.keys.Private != "" && s.keys.Public != "" {
		return nil
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("generate VAPID keys: %w", err)
	}
	s.keys = vapid{Private: priv, Public: pub}
	return writeJSON(path, s.keys)
}

// PublicKey is the VAPID application server key phones subscribe with.
func (s *Service) PublicKey() string { return s.keys.Public }

func (s *Service) save() error { return writeJSON(filepath.Join(s.dir, "push.json"), s.subs) }

// Subscribe registers (or replaces) a device's subscription.
func (s *Service) Subscribe(deviceID string, sub Subscription) error {
	if err := ValidEndpoint(sub.Endpoint); err != nil {
		return err
	}
	if sub.P256dh == "" || sub.Auth == "" {
		return errors.New("subscription keys are missing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.subs[deviceID]; !exists && len(s.subs) >= maxSubs {
		return errors.New("too many subscribed phones")
	}
	s.subs[deviceID] = sub
	return s.save()
}

// Unsubscribe removes a device's subscription; unknown devices are fine.
func (s *Service) Unsubscribe(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[deviceID]; !ok {
		return nil
	}
	delete(s.subs, deviceID)
	return s.save()
}

// Get returns a device's subscription.
func (s *Service) Get(deviceID string) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.subs[deviceID]
	return v, ok
}

// SetPrefs changes which notifications a subscribed device gets.
func (s *Service) SetPrefs(deviceID string, approvals, done bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.subs[deviceID]
	if !ok {
		return errors.New("this phone is not subscribed")
	}
	v.Approvals, v.Done = approvals, done
	s.subs[deviceID] = v
	return s.save()
}

// Approval notifies phones that want approval requests.
func (s *Service) Approval(m Message) {
	s.broadcast(m, true, func(v Subscription) bool { return v.Approvals })
}

// Done notifies phones that want to hear when an agent finishes.
func (s *Service) Done(m Message) { s.broadcast(m, false, func(v Subscription) bool { return v.Done }) }

// Test sends m to one device regardless of its preferences.
func (s *Service) Test(ctx context.Context, deviceID string, m Message) error {
	sub, ok := s.Get(deviceID)
	if !ok {
		return errors.New("this phone is not subscribed")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	status, err := s.sender.Send(ctx, sub, payload, true)
	if err != nil {
		return err
	}
	if gone(status) {
		_ = s.Unsubscribe(deviceID)
		return errors.New("the push service dropped this subscription; enable notifications again")
	}
	if status >= 300 {
		return fmt.Errorf("push service answered %d", status)
	}
	return nil
}

func gone(status int) bool { return status == http.StatusNotFound || status == http.StatusGone }

func (s *Service) broadcast(m Message, urgent bool, want func(Subscription) bool) {
	payload, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.mu.Lock()
	targets := make(map[string]Subscription, len(s.subs))
	for id, v := range s.subs {
		if want(v) {
			targets[id] = v
		}
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for id, v := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
			defer cancel()
			status, err := s.sender.Send(ctx, v, payload, urgent)
			if err == nil && gone(status) {
				_ = s.Unsubscribe(id) // the phone revoked permission or reinstalled
			}
		}()
	}
	wg.Wait()
}

type webSender struct {
	keys   vapid
	client *http.Client
}

func (w *webSender) Send(ctx context.Context, sub Subscription, payload []byte, urgent bool) (int, error) {
	urgency := webpush.UrgencyNormal
	if urgent {
		urgency = webpush.UrgencyHigh
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      w.client,
		Subscriber:      subscriber,
		VAPIDPublicKey:  w.keys.Public,
		VAPIDPrivateKey: w.keys.Private,
		TTL:             3600,
		Urgency:         urgency,
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// RemoveDevice drops a device's subscription when the device is revoked.
func (s *Service) RemoveDevice(deviceID string) { _ = s.Unsubscribe(deviceID) }

// Prune drops subscriptions of devices keep rejects (revoked phones).
func (s *Service) Prune(keep func(deviceID string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id := range s.subs {
		if !keep(id) {
			delete(s.subs, id)
			changed = true
		}
	}
	if changed {
		_ = s.save()
	}
}
