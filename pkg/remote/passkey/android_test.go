package passkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestNativeAndroidOriginsAreExplicitlyPinned(t *testing.T) {
	trusted := "android:apk-key-hash:" + b64.EncodeToString(bytes.Repeat([]byte{9}, 32))
	other := "android:apk-key-hash:" + b64.EncodeToString(bytes.Repeat([]byte{8}, 32))
	verifier := Verifier{RPID: rpID, Origin: origin, AndroidOrigins: []string{trusted}}
	a := newES256(t)
	challenge := Challenge("cid", "device", "digest", "nonce")
	cd, att := a.register(challenge, regOpts{org: trusted})
	if _, err := v.ParseRegistration(challenge, cd, att); !errors.Is(err, ErrInvalid) {
		t.Fatalf("native origin accepted without pin: %v", err)
	}
	cred, err := verifier.ParseRegistration(challenge, cd, att)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.VerifyAssertion(cred.Key, 0, challenge, a.assert(challenge, asOpts{org: trusted})); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []asOpts{{org: other}, {org: trusted, rp: "evil.example"}, {org: trusted, flags: flagUP}} {
		if _, err = verifier.VerifyAssertion(cred.Key, 0, challenge, a.assert(challenge, opts)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid native assertion %+v: %v", opts, err)
		}
	}
	cross := a.assert(challenge, asOpts{org: trusted})
	raw, _ := b64.DecodeString(cross.ClientDataJSON)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	data["crossOrigin"] = true
	raw, _ = json.Marshal(data)
	cross.ClientDataJSON = b64.EncodeToString(raw)
	if _, err = verifier.VerifyAssertion(cred.Key, 0, challenge, cross); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted cross-origin native assertion")
	}
	// Adding a signed native app must retain the existing browser origin.
	cd, att = a.register(challenge, regOpts{})
	if _, err = verifier.ParseRegistration(challenge, cd, att); err != nil {
		t.Fatal(err)
	}
}

func TestAndroidOriginRequiresCanonicalSHA256(t *testing.T) {
	valid := "android:apk-key-hash:" + b64.EncodeToString(make([]byte, 32))
	if !ValidAndroidOrigin(valid) {
		t.Fatal("canonical SHA256 rejected")
	}
	for _, bad := range []string{"", "https://app.example.dev", "android:apk-key-hash:*", valid + "=", "android:apk-key-hash:AAAA", valid + "/", "android:apk-key-hash:" + b64.EncodeToString(make([]byte, 31))} {
		if ValidAndroidOrigin(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}
