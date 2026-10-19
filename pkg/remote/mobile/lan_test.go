package mobile

import "testing"

func TestLANRelayConfig(t *testing.T) {
	for _, host := range []string{"10.0.0.2", "172.16.0.2", "172.31.255.254", "192.168.1.2"} {
		t.Run(host, func(t *testing.T) {
			cfg := Config{RelayURL: "http://" + host + ":8787"}
			if err := SaveConfig(t.TempDir(), cfg); err == nil {
				t.Fatal("LAN HTTP accepted without opt-in")
			}
			cfg.AllowLANHTTP = true
			dir := t.TempDir()
			if err := SaveConfig(dir, cfg); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig(dir)
			if err != nil || !loaded.AllowLANHTTP {
				t.Fatalf("opt-in not restored: %v", err)
			}
		})
	}
	for _, host := range []string{"8.8.8.8", "172.15.0.1", "172.32.0.1", "169.254.1.2", "relay.example"} {
		if err := SaveConfig(t.TempDir(), Config{RelayURL: "http://" + host, AllowLANHTTP: true}); err == nil {
			t.Fatalf("public/unsupported HTTP accepted: %s", host)
		}
	}
}
