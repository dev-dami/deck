package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/dev-dami/deck/internal/mobilecontrol"
)

func TestMobileConnectJSONStatus(t *testing.T) {
	t.Setenv("DECK_MOBILE_DIR", t.TempDir())
	command, _, err := mobileCmd.Find([]string{"connect"})
	if err != nil {
		t.Fatal(err)
	}
	command.SetContext(context.Background())
	if err := command.Flags().Set("action", "status"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Flags().Set("action", "start"); _ = command.Flags().Set("json", "false") }()
	var out bytes.Buffer
	command.SetOut(&out)
	defer command.SetOut(nil)
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	var result mobilecontrol.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout must contain only JSON: %q: %v", out.String(), err)
	}
	if result.Online || result.PairURL != "" || result.Message == "" {
		t.Fatalf("unexpected status: %+v", result)
	}
}
