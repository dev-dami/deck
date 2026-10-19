package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/vsh"
)

// The presets a max_scope may name are the sandbox's presets.
func TestOpsPresetsAreVshPresets(t *testing.T) {
	if got := slices.Sorted(slices.Values(config.OpsPresets)); !slices.Equal(got, vsh.PresetNames()) {
		t.Fatalf("config.OpsPresets = %v, vsh presets = %v", got, vsh.PresetNames())
	}
}

func TestOpsSandboxRunsInScratch(t *testing.T) {
	box, err := opsSandbox(nil)(nil, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, _, exit, _, err := box.Run(context.Background(), "echo hi > a.txt && cat a.txt")
	if err != nil || exit != 0 || out != "hi\n" {
		t.Fatalf("run = %q, exit %d, %v", out, exit, err)
	}
	_, errOut, exit, _, _ := box.Run(context.Background(), "cat /etc/passwd")
	if exit == 0 || !strings.Contains(errOut, "outside the scratch dir") {
		t.Fatalf("read outside scratch: exit %d %q", exit, errOut)
	}
	if _, err := opsSandbox(nil)([]string{"no-such-preset"}, nil, t.TempDir()); err == nil {
		t.Fatal("an unknown preset")
	}
}
