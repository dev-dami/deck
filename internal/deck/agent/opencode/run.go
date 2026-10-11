package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
)

// run is one live `opencode acp` process bound to one session.
type run struct {
	spec agent.Spec
	p    *proc

	events    chan agent.Event
	approvals chan agent.ApprovalRequest
	pushers   sync.WaitGroup // goroutines that may still send on approvals

	closing   chan struct{}
	closeOnce sync.Once
	loopDone  chan struct{}

	trMu sync.Mutex
	tr   *translator

	mu        sync.Mutex
	sessionID string
	busy      bool
	briefed   bool           // the spec's instructions went out with a prompt
	options   []configOption // the session's options as last reported
	loading   bool           // session/load is replaying history: drop updates
}

// turnEnded is queued on the client behind a turn's last updates (see
// codex.Client.Inject), so the turn ends after them.
const turnEnded = "deck/turnEnded"

type turnEnd struct {
	Result *promptResult `json:"result,omitempty"`
	Err    string        `json:"err,omitempty"`
}

func (r *run) Events() <-chan agent.Event              { return r.events }
func (r *run) Approvals() <-chan agent.ApprovalRequest { return r.approvals }

func (r *run) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

// mcpServers is Deck's MCP server in ACP's form. Its env (the hub token) goes
// to OpenCode over the protocol pipe, which only the two processes share.
func mcpServers(m *agent.MCPServer) []mcpServer {
	if m == nil || m.Name == "" || m.Command == "" {
		return []mcpServer{}
	}
	s := mcpServer{Name: m.Name, Command: m.Command, Args: append([]string{}, m.Args...), Env: []envVar{}}
	for k, v := range m.Env {
		s.Env = append(s.Env, envVar{Name: k, Value: v})
	}
	return []mcpServer{s}
}

// handshake opens (or resumes) the session, applies the spec's model, effort
// and mode, and starts the event loop.
func (r *run) handshake(ctx context.Context) error {
	ictx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	c := r.p.client
	sp := sessionParams{Cwd: r.spec.Dir, MCPServers: mcpServers(r.spec.MCP)}
	var res sessionResult
	go r.loop()
	switch {
	case r.spec.ResumeID == "":
		if err := c.Call(ictx, methodSessionNew, sp, &res); err != nil {
			return fmt.Errorf("opencode: session/new: %w", err)
		}
	case r.p.caps.AgentCapabilities.SessionCapabilities.Resume != nil:
		sp.SessionID = r.spec.ResumeID
		if err := c.Call(ictx, methodSessionResume, sp, &res); err != nil {
			return fmt.Errorf("opencode: session/resume: %w", err)
		}
		res.SessionID = r.spec.ResumeID
	default: // load replays the history as updates; Deck has its own
		sp.SessionID = r.spec.ResumeID
		r.mu.Lock()
		r.loading = true
		r.mu.Unlock()
		err := c.Call(ictx, methodSessionLoad, sp, &res)
		c.Inject(codex.Message{Method: "deck/loaded"})
		if err != nil {
			return fmt.Errorf("opencode: session/load: %w", err)
		}
		res.SessionID = r.spec.ResumeID
	}
	if res.SessionID == "" {
		return errors.New("opencode: no session id")
	}
	r.mu.Lock()
	r.sessionID = res.SessionID
	r.options = res.ConfigOptions
	r.mu.Unlock()

	if r.spec.Model != "" {
		if err := r.setOption(ictx, optModel, r.spec.Model); err != nil {
			return err
		}
	}
	if r.spec.Effort != "" && r.hasOption(optEffort) {
		_ = r.setOption(ictx, optEffort, r.spec.Effort)
	}
	if r.spec.Mode == agent.ModePlan && r.hasOption(optMode) {
		_ = r.setOption(ictx, optMode, modePlan)
	}

	raw, _ := json.Marshal(map[string]any{"sessionId": res.SessionID, "resumed": r.spec.ResumeID != ""})
	e := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindSessionStarted, SessionID: res.SessionID, Text: r.current(optModel), Raw: raw}
	r.events <- e
	// OpenCode's ACP reports no plan windows, and getting them some other
	// way would mean reading its stored credentials.
	r.events <- agent.UnsupportedLimits(r.spec.ID)
	return nil
}

// setOption sets one of the session's config options and keeps the options
// the answer reports.
func (r *run) setOption(ctx context.Context, id, value string) error {
	sid := r.SessionID()
	if sid == "" {
		return errNoSession
	}
	var res setOptionResult
	if err := r.p.client.Call(ctx, methodSessionSetOpt, setOptionParams{SessionID: sid, ConfigID: id, Value: value}, &res); err != nil {
		return fmt.Errorf("opencode: set %s: %w", id, err)
	}
	if len(res.ConfigOptions) > 0 {
		r.mu.Lock()
		r.options = res.ConfigOptions
		r.mu.Unlock()
	}
	return nil
}

func (r *run) option(id string) (configOption, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.options {
		if o.ID == id {
			return o, true
		}
	}
	return configOption{}, false
}

func (r *run) hasOption(id string) bool { _, ok := r.option(id); return ok }

func (r *run) current(id string) string { o, _ := r.option(id); return o.CurrentValue }

func (r *run) emit(e agent.Event) {
	select {
	case r.events <- e:
	case <-r.closing:
	}
}

// loop consumes everything the server sends until the process goes away.
func (r *run) loop() {
	defer close(r.loopDone)
	for m := range r.p.client.Incoming() {
		switch {
		case m.IsRequest():
			r.handleServerRequest(m)
		case m.Method == "deck/loaded":
			r.mu.Lock()
			r.loading = false
			r.mu.Unlock()
		case m.Method == turnEnded:
			r.endTurn(m.Params)
		case m.Method == notifySessionUpdate:
			var p updateParams
			if json.Unmarshal(m.Params, &p) != nil {
				continue
			}
			r.mu.Lock()
			loading := r.loading
			if p.Update.SessionUpdate == updConfigOptionUpdate && len(p.Update.ConfigOptions) > 0 {
				r.options = p.Update.ConfigOptions
			}
			r.mu.Unlock()
			if loading {
				continue
			}
			r.trMu.Lock()
			evs := r.tr.update(p.Update, m.Raw)
			r.trMu.Unlock()
			for _, e := range evs {
				r.emit(e)
			}
		}
	}

	// The stream ended: the process is gone (or was killed).
	r.p.stdin.Close()
	killTree(r.p.cmd)
	exit := "exited"
	if err := r.p.cmd.Wait(); err != nil {
		exit = err.Error()
	}
	if tail := r.p.stderr.String(); tail != "" {
		exit += ": " + tail
	}
	e := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindExited, Text: exit}
	select {
	case r.events <- e:
	case <-r.closing:
		select {
		case r.events <- e:
		default:
		}
	}
	r.pushers.Wait()
	close(r.approvals)
	close(r.events)
}

// endTurn ends the turn session/prompt answered: the streams are flushed, the
// usage reported and the turn marked done.
func (r *run) endTurn(params json.RawMessage) {
	var te turnEnd
	_ = json.Unmarshal(params, &te)
	r.trMu.Lock()
	evs := r.tr.flush(params)
	u := r.tr.usage()
	r.trMu.Unlock()
	if te.Err != "" {
		e := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindError, Text: te.Err}
		evs = append(evs, e)
	}
	done := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindTurnDone}
	if res := te.Result; res != nil {
		if res.Usage != nil {
			u.InputTokens, u.OutputTokens, u.CachedTokens = res.Usage.InputTokens, res.Usage.OutputTokens, res.Usage.CachedReadTokens
		}
		if res.StopReason == stopCancelled {
			done.Text = "interrupted"
		}
	}
	ue := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUsage, Usage: u}
	evs = append(evs, ue, done)
	r.mu.Lock()
	r.busy = false
	r.mu.Unlock()
	for _, e := range evs {
		r.emit(e)
	}
}

// Send starts a turn with the prompt.
func (r *run) Send(prompt string) error { return r.SendWithImages(prompt, nil) }

// SendWithImages starts a turn whose prompt is the images followed by the
// text. The first prompt of a process carries Deck's instructions ahead of it:
// ACP has no separate place for them.
func (r *run) SendWithImages(prompt string, images []agent.Image) error {
	sid := r.SessionID()
	if sid == "" {
		return errNoSession
	}
	var blocks []contentBlock
	for _, im := range images {
		data, err := os.ReadFile(im.Path)
		if err != nil {
			return fmt.Errorf("opencode: image: %w", err)
		}
		blocks = append(blocks, contentBlock{Type: "image", MimeType: im.MediaType, Data: base64.StdEncoding.EncodeToString(data)})
	}
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return errors.New("opencode: a turn is already running")
	}
	text := prompt
	if !r.briefed {
		text = "<deck-instructions>\n" + strings.TrimSpace(r.spec.Instructions) + "\n</deck-instructions>\n\n" + prompt
		r.briefed = true
	}
	r.busy = true
	r.mu.Unlock()
	blocks = append(blocks, contentBlock{Type: "text", Text: text})

	r.trMu.Lock()
	r.tr.beginTurn()
	r.trMu.Unlock()
	echo := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUserText, Text: prompt}
	r.emit(echo)

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-r.closing:
				cancel()
			case <-ctx.Done():
			}
		}()
		var res promptResult
		te := turnEnd{}
		if err := r.p.client.Call(ctx, methodSessionPrompt, promptParams{SessionID: sid, Prompt: blocks}, &res); err != nil {
			te.Err = err.Error()
		} else {
			te.Result = &res
		}
		params, _ := json.Marshal(te)
		r.p.client.Inject(codex.Message{Method: turnEnded, Params: params})
	}()
	return nil
}

// Interrupt cancels the running turn; session/prompt then ends "cancelled".
func (r *run) Interrupt() error {
	r.mu.Lock()
	sid, busy := r.sessionID, r.busy
	r.mu.Unlock()
	if sid == "" || !busy {
		return nil
	}
	return r.p.client.Notify(notifySessionCancel, cancelParams{SessionID: sid})
}

// Close kills the process tree. Safe to call more than once.
func (r *run) Close() error {
	r.closeOnce.Do(func() { close(r.closing) })
	_ = r.p.stdin.Close()
	killTree(r.p.cmd)
	return nil
}

// watchContext ties the process lifetime to the context passed to Start.
func (r *run) watchContext(ctx context.Context) {
	select {
	case <-ctx.Done():
		r.Close()
	case <-r.loopDone:
	}
}

// SetMode switches OpenCode between its plan and build agents. Deck enforces
// the mode on approvals itself either way.
func (r *run) SetMode(m agent.Mode) error {
	if !r.hasOption(optMode) {
		return nil
	}
	v := modeBuild
	if m == agent.ModePlan {
		v = modePlan
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.setOption(ctx, optMode, v)
}

// SetModel switches the session's model ("provider/model"); it applies from
// the next turn.
func (r *run) SetModel(model string) error {
	if model == "" {
		return errors.New("opencode: empty model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.setOption(ctx, optModel, model)
}

// SetEffort sets the model's reasoning effort ("" = its default). A model
// without an effort setting ignores it.
func (r *run) SetEffort(effort string) error {
	if !r.hasOption(optEffort) {
		if effort == "" {
			return nil
		}
		return errors.New("opencode: this model has no effort setting")
	}
	if effort == "" {
		effort = effortDefault
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.setOption(ctx, optEffort, effort)
}

var (
	_ agent.ModeSetter   = (*run)(nil)
	_ agent.ModelSetter  = (*run)(nil)
	_ agent.EffortSetter = (*run)(nil)
	_ agent.ImageSender  = (*run)(nil)
)
