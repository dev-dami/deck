package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/config"
)

func TestParseOps(t *testing.T) {
	cases := []struct {
		in      string
		peer    string
		scope   []string
		fields  []string
		targets []string
		err     string
	}{
		{in: "ask vps to report podman containers: name, state, uptime, restarts", peer: "vps",
			scope: []string{"podman-read"}, fields: []string{"name", "state", "uptime", "restarts"}},
		{in: "Ask VPS to Report Podman Containers: Name, State for web, DB-1", peer: "vps",
			scope: []string{"podman-read"}, fields: []string{"name", "state"}, targets: []string{"web", "DB-1"}},
		{in: "  ask   vps  to report services:name ,  active state  ", peer: "vps",
			scope: []string{"systemd-read"}, fields: []string{"name", "active_state"}},
		{in: "ask vps to report containers and systemd: name for nginx.service", peer: "vps",
			scope: []string{"podman-read", "systemd-read"}, fields: []string{"name"}, targets: []string{"nginx.service"}},
		{in: "ask vps to report podman, containers: name", peer: "vps", scope: []string{"podman-read"}, fields: []string{"name"}},
		{in: "tell vps to report podman: name", err: "write: ask"},
		{in: "ask vps report podman: name", err: "write: ask"},
		{in: "ask vps to report podman name", err: "colon"},
		{in: "ask vps to report disks: name", err: `"disks" is not something`},
		{in: "ask vps to report : name", err: "say what to report"},
		{in: "ask vps to report podman:", err: "1 to 20 fields"},
		{in: "ask vps to report podman: name, name", err: "twice"},
		{in: "ask vps to report podman: 1st", err: "cannot be a field name"},
		{in: "ask vps to report podman: name for web; rm -rf /", err: "cannot be a target"},
		{in: "ask a/b to report podman: name", err: "not a teammate's name"},
	}
	for _, tc := range cases {
		name, c, err := ParseOps(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: error %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		var fields []string
		for _, f := range c.Schema {
			fields = append(fields, f.Name)
			if f.Type != FieldString || f.MaxLength != 200 {
				t.Errorf("%q: field %+v", tc.in, f)
			}
		}
		if name != tc.peer || c.Kind != KindOps || !reflect.DeepEqual(c.Scope, tc.scope) || !reflect.DeepEqual(fields, tc.fields) || !reflect.DeepEqual(c.Targets, tc.targets) {
			t.Errorf("%q: %s %+v", tc.in, name, c)
		}
	}
}

func TestCheckResult(t *testing.T) {
	schema := []Field{{Name: "name", Type: FieldString, MaxLength: 5}, {Name: "restarts", Type: FieldNumber}, {Name: "up", Type: FieldBool}, {Name: "note", Type: FieldString}}
	ok := []struct{ in, want string }{
		{`{"rows": []}`, `{"rows":[]}`},
		{`{"rows": [{"name": "web", "restarts": 2, "up": true}]}`, `{"rows":[{"name":"web","restarts":2,"up":true}]}`},
		{`{"rows": [{"name": null}, {}]}`, `{"rows":[{"name":null},{}]}`},
		{`{"rows": [{"note": "a\u001b[31mb\nc"}]}`, `{"rows":[{"note":"a[31mb c"}]}`},
		{`{"rows": [{"note": "key ghp_abcdefghijklmnopqrstuvwxyz0123 here"}]}`, `{"rows":[{"note":"key [redacted] here"}]}`},
	}
	for _, tc := range ok {
		got, err := CheckResult(schema, json.RawMessage(tc.in))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: %s %v", tc.in, got, err)
		}
	}
	bad := []struct{ in, want string }{
		{`[]`, "JSON object"},
		{`null`, "JSON object"},
		{`{"rows": [], "extra": 1}`, `property "extra"`},
		{`{"rows": {}}`, "list of objects"},
		{`{}`, "list of objects"},
		{`{"rows": [1]}`, "list of objects"},
		{`{"rows": [null]}`, "row 1 is not an object"},
		{`{"rows": [{"name": "web", "image": "x"}]}`, `row 1 has the property "image"`},
		{`{"rows": [{"name": 5}]}`, "name must be a string"},
		{`{"rows": [{"restarts": "5"}]}`, "restarts must be a number"},
		{`{"rows": [{"up": "yes"}]}`, "up must be true or false"},
		{`{"rows": [{"name": "toolong"}]}`, "longer than 5 characters"},
		{`{"rows": [{}, {"note": "` + strings.Repeat("x", 201) + `"}]}`, "row 2: note is longer than 200"},
		{`{"rows": [` + strings.TrimSuffix(strings.Repeat(`{},`, 201), ",") + `]}`, "201 rows"},
		{`{"rows": [{"note": "` + strings.Repeat("x", 70<<10) + `"}]}`, "larger than 64 KB"},
	}
	for _, tc := range bad {
		if _, err := CheckResult(schema, json.RawMessage(tc.in)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%.80s: %v, want %q", tc.in, err, tc.want)
		}
	}
}

// b asks a for a report. A scope outside the max_scope a gave b is refused
// with the reason; one inside runs at once, without a's person, and its
// result comes back, checked by both Decks. Every step is in the audit log.
func TestOpsContractScopeAndResult(t *testing.T) {
	ctx := ctxT(t)
	ran := make(chan Contract, 4)
	run := func(c Contract) (string, string, func(), error) {
		if len(c.Targets) > 0 && c.Targets[0] == "bad" {
			return "", "", nil, os.ErrPermission
		}
		ran <- c
		return "th-" + c.ID, "t1", nil, nil
	}
	news := make(chan Contract, 8)
	a, b := linkedWith(t,
		ServiceOptions{Contracts: ContractHooks{Run: run}},
		ServiceOptions{Contracts: ContractHooks{Updated: func(c Contract) { news <- c }}},
		config.PeerRoleContractor)
	if _, err := config.UpdatePeers(a.cfgPath, func(p *config.Peers) bool {
		p.Remote = append(p.Remote, config.PeerRemote{Name: "b", Role: config.PeerRoleContractor, MaxScope: []string{"podman-read"}})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}

	_, c, err := ParseOps("ask a to report services: name, state")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ProposeContract(ctx, "a", c); err == nil || !strings.Contains(err.Error(), "systemd-read is outside the scope this Deck allows you (max_scope: podman-read)") {
		t.Fatalf("out of scope: %v", err)
	}
	if len(a.Contracts())+len(b.Contracts()) != 0 || len(ran) != 0 {
		t.Fatalf("a refused contract left records or ran: %+v %+v", a.Contracts(), b.Contracts())
	}

	_, c, _ = ParseOps("ask a to report podman containers: name for bad")
	if _, err := b.ProposeContract(ctx, "a", c); err == nil || !strings.Contains(err.Error(), "cannot run ops contracts now") {
		t.Fatalf("a run that fails: %v", err)
	}

	_, c, _ = ParseOps("ask a to report podman containers: name, state, restarts for web, db")
	sent, err := b.ProposeContract(ctx, "a", c)
	if err != nil {
		t.Fatal(err)
	}
	k := <-ran
	if !k.Inbound || k.Kind != KindOps || k.Title != "report podman containers" || !reflect.DeepEqual(k.Targets, []string{"web", "db"}) || len(k.Schema) != 3 {
		t.Fatalf("a runs %+v", k)
	}
	if got := waitNews(t, news); got.State != ContractAccepted || got.ID != sent.ID {
		t.Fatalf("accepted news = %+v", got)
	}
	result := json.RawMessage(`{"rows": [{"name": "web", "state": "running", "restarts": "0"}, {"name": "db", "state": "exited", "restarts": null}]}`)
	if a.FinishOps("th-"+k.ID, "t2", result, "") {
		t.Fatal("finished a task that is no contract's")
	}
	if !a.FinishOps("th-"+k.ID, "t1", result, "") {
		t.Fatal("the contract's task was not found")
	}
	done := waitNews(t, news)
	want := `{"rows":[{"name":"web","restarts":"0","state":"running"},{"name":"db","restarts":null,"state":"exited"}]}`
	if done.State != ContractDone || string(done.Result) != want {
		t.Fatalf("done news = %+v %s", done, done.Result)
	}

	// b checks the result again: one that does not fit its schema fails.
	_, c, _ = ParseOps("ask a to report podman: name")
	sent, err = b.ProposeContract(ctx, "a", c)
	if err != nil {
		t.Fatal(err)
	}
	<-ran
	waitNews(t, news)
	args, _ := json.Marshal(map[string]any{"id": sent.ID, "state": ContractDone, "result": json.RawMessage(`{"rows": [{"name": "web", "env": "SECRET=1"}]}`)})
	if _, err := b.reported(Caller{Name: "a"}, OpContractResult, args); err != nil {
		t.Fatal(err)
	}
	failed := waitNews(t, news)
	if failed.State != ContractFailed || failed.Result != nil || !strings.Contains(failed.Note, `does not fit the contract: row 1 has the property "env"`) {
		t.Fatalf("a result that does not fit = %+v", failed)
	}

	audit := func(s *Service) string {
		raw, err := os.ReadFile(filepath.Join(s.dir, "audit.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, w := range []string{`"event":"refused","detail":"systemd-read is outside`, `"event":"refused","detail":"permission denied"`, `"event":"accepted"`, `"event":"result sent"`} {
		if !strings.Contains(audit(a), w) {
			t.Errorf("a's audit misses %s:\n%s", w, audit(a))
		}
	}
	for _, w := range []string{`"event":"proposed"`, `"event":"accepted"`, `"event":"result done"`, `"event":"result failed"`} {
		if !strings.Contains(audit(b), w) {
			t.Errorf("b's audit misses %s:\n%s", w, audit(b))
		}
	}
}

func TestAuditLogKeepsTheLastLines(t *testing.T) {
	dir := t.TempDir()
	l := newAuditLog(dir)
	for i := 0; i < maxAuditLines+10; i++ {
		l.add(AuditEntry{Peer: "vps", Event: "sh.run"})
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n > maxAuditLines || n < maxAuditLines*4/5 {
		t.Fatalf("%d lines kept", n)
	}
	// A new log counts the lines already there.
	l = newAuditLog(dir)
	l.add(AuditEntry{Event: "x"})
	if l.lines != strings.Count(string(raw), "\n")+1 {
		t.Fatalf("lines = %d", l.lines)
	}
}

// An ops contract that ran when a stopped fails when a starts again, and
// its requester hears why. One that ended before stays as it is.
func TestOpsContractFailsAfterARestart(t *testing.T) {
	ctx := ctxT(t)
	run := func(c Contract) (string, string, func(), error) { return "th-" + c.ID, "t1", nil, nil }
	news := make(chan Contract, 8)
	a, b := linkedWith(t,
		ServiceOptions{Contracts: ContractHooks{Run: run}},
		ServiceOptions{Contracts: ContractHooks{Updated: func(c Contract) { news <- c }}},
		config.PeerRoleContractor)
	if _, err := config.UpdatePeers(a.cfgPath, func(p *config.Peers) bool {
		p.Remote = append(p.Remote, config.PeerRemote{Name: "b", Role: config.PeerRoleContractor, MaxScope: []string{"podman-read"}})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	propose := func() Contract {
		_, c, _ := ParseOps("ask a to report podman containers: name")
		sent, err := b.ProposeContract(ctx, "a", c)
		if err != nil {
			t.Fatal(err)
		}
		if got := waitNews(t, news); got.State != ContractAccepted || got.ID != sent.ID {
			t.Fatalf("accepted news = %+v", got)
		}
		return sent
	}
	ended := propose()
	if !a.FinishOps("th-"+ended.ID, "t1", nil, "its person cancelled it") {
		t.Fatal("the first contract did not end")
	}
	waitNews(t, news)
	running := propose()

	// a stops, and starts again on the same files.
	a.Close()
	a2, err := NewService(ServiceOptions{Dir: a.dir, ConfigPath: a.cfgPath, Logger: quietLog, Contracts: ContractHooks{Run: run}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a2.Close)
	p, err := config.LoadPeers(a.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := a2.Start(p); err != nil {
		t.Fatal(err)
	}
	const why = "Deck restarted while it ran"
	if n := a2.FailRunningOps(why); n != 1 {
		t.Fatalf("failed %d contracts, want 1", n)
	}
	for _, c := range a2.Contracts() {
		if c.State != ContractFailed || c.ID == running.ID && c.Note != why || c.ID == ended.ID && c.Note != "its person cancelled it" {
			t.Fatalf("after the restart: %+v", c)
		}
	}
	if got := waitNews(t, news); got.ID != running.ID || got.State != ContractFailed || got.Note != why {
		t.Fatalf("b hears %+v", got)
	}
	if n := a2.FailRunningOps("again"); n != 0 {
		t.Fatalf("failed %d contracts the second time", n)
	}
}
