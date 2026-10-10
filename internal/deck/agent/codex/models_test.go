package codex

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// modelListScript uses a ModelListResponse-shaped result (schema v2/ModelListResponse.json).
func modelListScript(t *testing.T, withList bool) string {
	lines := []string{
		send(`{"id":1,"method":"initialize","params":{}}`),
		recv(`{"id":1,"result":{"userAgent":"x"}}`),
	}
	if withList {
		lines = append(lines,
			send(`{"id":2,"method":"model/list","params":{}}`),
			recv(`{"id":2,"result":{"data":[`+
				`{"id":"gpt-a","model":"gpt-a","displayName":"GPT A","description":"fast one","hidden":false,"isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"low","description":"l"}]},`+
				`{"id":"gpt-b-id","model":"gpt-b","displayName":"","description":"","hidden":false,"isDefault":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[]},`+
				`{"id":"secret","model":"secret","displayName":"Hidden","description":"x","hidden":true,"isDefault":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[]}`+
				`],"nextCursor":null}}`),
		)
	}
	return writeScript(t, lines...)
}

func fakeAdapter(t *testing.T, fe fakeEnv) agent.ModelLister {
	t.Helper()
	for k, v := range fe.env {
		t.Setenv(k, v) // the short-lived server inherits the test process environment
	}
	l, ok := New(os.Args[0]).(agent.ModelLister)
	if !ok {
		t.Fatal("adapter is not a ModelLister")
	}
	return l
}

func TestModelsMapping(t *testing.T) {
	fe := newFakeEnv(t, modelListScript(t, true))
	l := fakeAdapter(t, fe)
	got, err := l.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.ModelInfo{
		{ID: "gpt-a", Name: "GPT A", Description: "fast one", Default: true,
			Efforts: []agent.EffortInfo{{Level: "low", Description: "l"}}, DefaultEffort: "medium"},
		{ID: "gpt-b", Name: "gpt-b", DefaultEffort: "low"}, // model slug is the id; empty displayName falls back
	}
	if len(got) != len(want) {
		t.Fatalf("models = %+v", got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("model %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !strings.Contains(fe.read(t), "model/list=") {
		t.Error("model/list not requested")
	}

	// Cached: no second app-server is started.
	if _, err := l.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(fe.read(t), "model/list="); n != 1 {
		t.Errorf("model/list requested %d times, want 1 (cache)", n)
	}
}

func TestModelsTimeout(t *testing.T) {
	old := modelListTimeout
	modelListTimeout = 500 * time.Millisecond
	t.Cleanup(func() { modelListTimeout = old })

	fe := newFakeEnv(t, modelListScript(t, false)) // fake never answers model/list
	l := fakeAdapter(t, fe)
	start := time.Now()
	_, err := l.Models(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	// Errors are not cached: a retry runs again. (Checked on the cache itself;
	// counting model/list requests is timing-dependent, since under load the
	// fake server can still be starting when the 500ms timeout fires.)
	if a := l.(*adapter); a.models != nil {
		t.Fatalf("a failed listing was cached: %+v", a.models)
	}
	if _, err := l.Models(context.Background()); err == nil {
		t.Fatal("expected second timeout error")
	}
}

func TestModelsMissingBinary(t *testing.T) {
	l := New("definitely-not-a-real-codex-binary").(agent.ModelLister)
	if _, err := l.Models(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// TestLiveModels lists the real models through the installed codex. It makes
// no model call and prints only the count and ids.
func TestLiveModels(t *testing.T) {
	if os.Getenv("DECK_LIVE") == "" {
		t.Skip("set DECK_LIVE=1 to run against the installed codex")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex not installed")
	}
	ms, err := New("codex").(agent.ModelLister).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no models")
	}
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	t.Logf("%d models: %s", len(ms), strings.Join(ids, ", "))
}
