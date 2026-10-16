package mobile

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestAndroidOriginConfigRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	origin := "android:apk-key-hash:" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	cfg := Config{RelayURL: "https://app.example.dev", AndroidOrigins: []string{origin}}
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(dir)
	if err != nil || len(got.AndroidOrigins) != 1 || got.AndroidOrigins[0] != origin {
		t.Fatalf("roundtrip %+v %v", got, err)
	}
	cfg.AndroidOrigins = []string{"android:apk-key-hash:*"}
	if err = SaveConfig(dir, cfg); err == nil {
		t.Fatal("wildcard trusted")
	}
	if err = os.WriteFile(filepath.Join(dir, configFile), []byte(`{"android_origins":["https://evil.example"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfig(dir); err == nil {
		t.Fatal("manual untrusted origin accepted")
	}
}
