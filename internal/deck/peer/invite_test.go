package peer

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

const testFP = "SHA256:jaSK+FcF9OBTJiSft2QoKvM2UHimsBx3eM7RWdM7RAQ"

func testInvite() Invite {
	return Invite{
		Host: "203.0.113.5", Alt: []string{"10.0.0.5", "vps.example.com"}, Port: "7722",
		Code: "BXSV4-4XRZC", Fingerprint: testFP, Name: "vps", Expires: time.Unix(1760000000, 0),
	}
}

func TestInviteRoundTrip(t *testing.T) {
	inv := testInvite()
	line := inv.Encode()
	if !strings.HasPrefix(line, "deck://pair?v=1&host=203.0.113.5&port=7722&code=BXSV4-4XRZC&fp=") {
		t.Fatalf("encoded: %s", line)
	}
	if strings.ContainsAny(line, " \n\t") {
		t.Fatalf("not one line: %q", line)
	}
	got, err := ParseInvite("  " + line + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != inv.Host || got.Port != inv.Port || got.Code != inv.Code || got.Fingerprint != inv.Fingerprint ||
		got.Name != inv.Name || !got.Expires.Equal(inv.Expires) || strings.Join(got.Alt, ",") != strings.Join(inv.Alt, ",") {
		t.Fatalf("round trip: %+v != %+v", got, inv)
	}
	if got.Addr() != "203.0.113.5:7722" {
		t.Fatalf("addr %s", got.Addr())
	}
	// IPv6 hosts and a lower-case code survive too.
	inv.Host, inv.Code, inv.Name, inv.Alt, inv.Expires = "2001:db8::5", "bxsv4-4xrzc", "", nil, time.Time{}
	got, err = ParseInvite(inv.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr() != "[2001:db8::5]:7722" || got.Code != "BXSV4-4XRZC" {
		t.Fatalf("v6: %+v", got)
	}
	if !LooksLikeInvite(" DECK://pair?x") || LooksLikeInvite("BXSV4-4XRZC") {
		t.Fatal("LooksLikeInvite")
	}
}

func TestInviteHostile(t *testing.T) {
	good := testInvite().Encode()
	cases := map[string]string{
		"empty":           "",
		"plain code":      "BXSV4-4XRZC",
		"other scheme":    strings.Replace(good, "deck://pair?", "https://pair?", 1),
		"other version":   strings.Replace(good, "v=1", "v=2", 1),
		"no version":      strings.Replace(good, "v=1&", "", 1),
		"too long":        good + "&alt=" + strings.Repeat("a", 2000),
		"newline inside":  strings.Replace(good, "port=7722", "port=7722\n", 1),
		"escaped newline": strings.Replace(good, "name=vps", "name=vps%0A", 1),
		"escaped escape":  strings.Replace(good, "name=vps", "name=%1B%5B31m", 1),
		"bidi override":   strings.Replace(good, "name=vps", "name=%E2%80%AEvps", 1),
		"bad escape":      strings.Replace(good, "name=vps", "name=%zz", 1),
		"unknown field":   good + "&cmd=rm",
		"repeated code":   good + "&code=BXSV4-4XRZC",
		"port zero":       strings.Replace(good, "port=7722", "port=0", 1),
		"port too big":    strings.Replace(good, "port=7722", "port=70000", 1),
		"port padded":     strings.Replace(good, "port=7722", "port=07722", 1),
		"port word":       strings.Replace(good, "port=7722", "port=ssh", 1),
		"short code":      strings.Replace(good, "code=BXSV4-4XRZC", "code=BXSV4", 1),
		"code alphabet":   strings.Replace(good, "code=BXSV4-4XRZC", "code=0OIL1-UUUUU", 1),
		"no fingerprint":  strings.Replace(good, "&fp=", "&nofp=", 1),
		"md5 fingerprint": strings.Replace(good, "fp=SHA256", "fp=MD5", 1),
		"host with path":  strings.Replace(good, "host=203.0.113.5", "host=evil.com%2Fx", 1),
		"host with user":  strings.Replace(good, "host=203.0.113.5", "host=a%40b", 1),
		"unspecified":     strings.Replace(good, "host=203.0.113.5", "host=0.0.0.0", 1),
		"bad name":        strings.Replace(good, "name=vps", "name=..%2F..", 1),
		"bad expiry":      strings.Replace(good, "exp=1760000000", "exp=-5", 1),
		"too many alts":   good + "&alt=a.b&alt=c.d",
	}
	for name, s := range cases {
		if _, err := ParseInvite(s); !errors.Is(err, ErrNotInvite) {
			t.Errorf("%s: %v, want ErrNotInvite", name, err)
		} else if strings.Contains(err.Error(), "BXSV4") {
			t.Errorf("%s: the error repeats the code: %v", name, err)
		}
	}
}

func TestInviteHostsChoice(t *testing.T) {
	ifaces := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("169.254.3.4"), Mask: net.CIDRMask(16, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::7"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("10.0.0.5"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	host := func() (string, error) { return "VPS-1", nil }
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "SSH_CONNECTION" {
				return v
			}
			return ""
		}
	}
	src := hostSources{env: env("198.51.100.9 50000 203.0.113.5 22"), addrs: ifaces, hostname: host}
	got := src.inviteHosts("0.0.0.0:7722")
	if strings.Join(got, ",") != "203.0.113.5,10.0.0.5,2001:db8::7" {
		t.Fatalf("with SSH_CONNECTION: %v", got)
	}
	src.env = env("")
	if got := src.inviteHosts(":7722"); strings.Join(got, ",") != "10.0.0.5,2001:db8::7" {
		t.Fatalf("interfaces only: %v", got)
	}
	src.env = env("garbage")
	src.addrs = func() ([]net.Addr, error) { return nil, errors.New("no") }
	if got := src.inviteHosts(":7722"); strings.Join(got, ",") != "vps-1" {
		t.Fatalf("host name fallback: %v", got)
	}
	// A listener bound to one address is reached by that address only.
	src.env = env("198.51.100.9 50000 203.0.113.5 22")
	if got := src.inviteHosts("10.8.0.1:7722"); strings.Join(got, ",") != "10.8.0.1" {
		t.Fatalf("bound address: %v", got)
	}
}

func TestPairWaitInvite(t *testing.T) {
	w := &PairWait{Code: "BXSV4-4XRZC", Addr: "0.0.0.0:7722", Fingerprint: testFP, Name: "vps",
		Hosts: []string{"203.0.113.5", "10.0.0.5"}, Expires: time.Unix(1760000000, 0)}
	inv, err := ParseInvite(w.Invite().Encode())
	if err != nil {
		t.Fatal(err)
	}
	if inv.Addr() != "203.0.113.5:7722" || inv.Code != w.Code || inv.Fingerprint != testFP || inv.Alt[0] != "10.0.0.5" {
		t.Fatalf("invite: %+v", inv)
	}
}
