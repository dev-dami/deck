package peer

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dev-dami/deck/internal/deck/config"
)

// Invite is everything the connecting Deck needs to pair, as one line to
// copy from the waiting Deck and paste on the other:
//
//	deck://pair?v=1&host=203.0.113.5&port=7722&code=ABCDE-23456&fp=SHA256%3A...&name=vps&exp=1760000000
//
// It holds the one-time code, so it is a secret for its five minutes: it is
// never logged and never part of a Snapshot or an error. The fingerprint
// lets the connecting Deck check the key it reaches before the code is used.
type Invite struct {
	Host        string
	Alt         []string // other addresses of the same Deck, best first
	Port        string
	Code        string // XXXXX-XXXXX
	Fingerprint string // SHA256:...
	Name        string // the waiting Deck's name; may be empty
	Expires     time.Time
}

// InvitePrefix starts every invite.
const InvitePrefix = "deck://pair?"

const (
	inviteVersion = "1"
	maxInviteLen  = 1024
	maxInviteAlts = 3
)

// ErrNotInvite is returned by ParseInvite for text that is not a Deck
// invite; the wrapped reason says what is wrong (never the code).
var ErrNotInvite = errors.New("not a Deck invite")

var (
	inviteHostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	inviteFPRE   = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

// Addr is host:port to dial.
func (i Invite) Addr() string { return net.JoinHostPort(i.Host, i.Port) }

// Encode writes the invite as one line, its fields in a fixed order.
func (i Invite) Encode() string {
	var b strings.Builder
	b.WriteString(InvitePrefix)
	add := func(k, v string) {
		if b.Len() > len(InvitePrefix) {
			b.WriteByte('&')
		}
		b.WriteString(k + "=" + url.QueryEscape(v))
	}
	add("v", inviteVersion)
	add("host", i.Host)
	add("port", i.Port)
	add("code", i.Code)
	add("fp", i.Fingerprint)
	if i.Name != "" {
		add("name", i.Name)
	}
	if !i.Expires.IsZero() {
		add("exp", strconv.FormatInt(i.Expires.Unix(), 10))
	}
	for _, a := range i.Alt {
		add("alt", a)
	}
	return b.String()
}

// LooksLikeInvite reports whether s is meant as an invite (it may still not
// parse), as opposed to a plain code or address.
func LooksLikeInvite(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "deck://")
}

// ParseInvite reads an invite strictly: version 1 only, every field checked,
// no unknown or repeated fields, no control or format characters, at most
// maxInviteLen bytes. Errors wrap ErrNotInvite.
func ParseInvite(s string) (Invite, error) {
	bad := func(why string) (Invite, error) { return Invite{}, fmt.Errorf("%w: %s", ErrNotInvite, why) }
	s = strings.TrimSpace(s)
	if len(s) > maxInviteLen {
		return bad("too long")
	}
	if hasControl(s) {
		return bad("control characters")
	}
	if !strings.HasPrefix(strings.ToLower(s), InvitePrefix) {
		return bad("it does not start with " + InvitePrefix)
	}
	q, err := url.ParseQuery(s[len(InvitePrefix):])
	if err != nil {
		return bad("badly escaped")
	}
	var inv Invite
	for k, vs := range q {
		for _, v := range vs {
			if hasControl(v) {
				return bad("control characters")
			}
		}
		if k != "alt" && len(vs) != 1 {
			return bad("repeated " + truncate(k, 16))
		}
		switch k {
		case "v":
			if vs[0] != inviteVersion {
				return bad("unknown version")
			}
		case "host":
			inv.Host = vs[0]
		case "port":
			inv.Port = vs[0]
		case "code":
			inv.Code = vs[0]
		case "fp":
			inv.Fingerprint = vs[0]
		case "name":
			inv.Name = vs[0]
		case "exp":
			n, err := strconv.ParseInt(vs[0], 10, 64)
			if err != nil || n <= 0 {
				return bad("bad expiry")
			}
			inv.Expires = time.Unix(n, 0)
		case "alt":
			if len(vs) > maxInviteAlts {
				return bad("too many addresses")
			}
			inv.Alt = vs
		default:
			return bad("unknown field")
		}
	}
	if q.Get("v") == "" {
		return bad("no version")
	}
	if !validInviteHost(inv.Host) {
		return bad("bad host")
	}
	for _, a := range inv.Alt {
		if !validInviteHost(a) {
			return bad("bad host")
		}
	}
	if p, err := strconv.Atoi(inv.Port); err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != inv.Port {
		return bad("bad port")
	}
	code := normalizeCode(inv.Code)
	if !validCode(code) {
		return bad("bad code")
	}
	inv.Code = code[:5] + "-" + code[5:]
	if !inviteFPRE.MatchString(inv.Fingerprint) {
		return bad("bad fingerprint")
	}
	if inv.Name != "" && !config.ValidPeerName(inv.Name) {
		return bad("bad name")
	}
	return inv, nil
}

// validInviteHost accepts an IP address or a DNS name.
func validInviteHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsUnspecified()
	}
	return inviteHostRE.MatchString(h)
}

// hasControl reports control and Unicode format characters (bidi overrides,
// zero-width), which have no place in an invite.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar {
			return true
		}
	}
	return false
}

// hostSources is where InviteHosts looks; tests replace it.
type hostSources struct {
	env      func(string) string
	addrs    func() ([]net.Addr, error)
	hostname func() (string, error)
}

var systemHosts = hostSources{env: os.Getenv, addrs: net.InterfaceAddrs, hostname: os.Hostname}

// InviteHosts guesses the addresses another machine can reach a listener
// bound to bound by, best first; a listener does not know its public
// address. A listener bound to one address is reached by that address.
// Otherwise: the server address of $SSH_CONNECTION, when Deck runs in an SSH
// session (the address the person reached this machine by), then the
// non-loopback, non-link-local unicast addresses of the interfaces (IPv4
// first), then the host name.
func InviteHosts(bound string) []string { return systemHosts.inviteHosts(bound) }

func (src hostSources) inviteHosts(bound string) []string {
	var out []string
	add := func(h string) {
		if h == "" || !validInviteHost(h) {
			return
		}
		for _, o := range out {
			if o == h {
				return
			}
		}
		out = append(out, h)
	}
	if host, _, err := net.SplitHostPort(bound); err == nil {
		if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
			return []string{ip.String()}
		}
	}
	if f := strings.Fields(src.env("SSH_CONNECTION")); len(f) == 4 {
		if ip := net.ParseIP(f[2]); usableIP(ip) {
			add(ip.String())
		}
	}
	if src.addrs != nil {
		if addrs, err := src.addrs(); err == nil {
			var v6 []string
			for _, a := range addrs {
				n, ok := a.(*net.IPNet)
				if !ok || !usableIP(n.IP) {
					continue
				}
				if n.IP.To4() != nil {
					add(n.IP.String())
				} else {
					v6 = append(v6, n.IP.String())
				}
			}
			for _, h := range v6 {
				add(h)
			}
		}
	}
	if len(out) == 0 && src.hostname != nil {
		if h, err := src.hostname(); err == nil {
			add(strings.ToLower(h))
		}
	}
	return out
}

// usableIP is an address another machine could use: unicast, not loopback,
// not link-local.
func usableIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast()
}

// Invite is the invite for this wait: the best of Hosts as the host, the
// rest as alternatives.
func (w *PairWait) Invite() Invite {
	inv := Invite{
		Port: portOfAddr(w.Addr), Code: w.Code, Fingerprint: w.Fingerprint,
		Name: w.Name, Expires: w.Expires,
	}
	if !config.ValidPeerName(inv.Name) {
		inv.Name = ""
	}
	if len(w.Hosts) > 0 {
		inv.Host = w.Hosts[0]
		inv.Alt = w.Hosts[1:min(len(w.Hosts), 1+maxInviteAlts)]
	}
	return inv
}
