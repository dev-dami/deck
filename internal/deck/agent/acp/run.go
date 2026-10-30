package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
)

// run is one live agent process bound to one session.
type run struct {
	a    *Adapter
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

	mu         sync.Mutex
	sessionID  string
	busy       bool
	briefed    bool           // the spec's instructions went out with a prompt
	options    []configOption // the session's options as last reported
	loading    bool           // session/load is replaying history: drop updates
	baseEffort string         // the effort the session started with: "" = the model's default
	banner     string         // a startup banner the agent announced and has not sent yet
}

// turnEnded is queued on the client behind a turn's last updates (see
// codex.Client.Inject), so the turn ends after them; turnStarted ahead of
// its first, so what came before is known to be outside it.
const (
	turnEnded   = "deck/turnEnded"
	turnStarted = "deck/turnStarted"
)

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
// to the agent over the protocol pipe, which only the two processes share.
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
			return r.a.errorf("session/new: %w", err)
		}
	case r.p.caps.AgentCapabilities.SessionCapabilities.Resume != nil:
		sp.SessionID = r.spec.ResumeID
		if err := c.Call(ictx, methodSessionResume, sp, &res); err != nil {
			return r.a.errorf("session/resume: %w", err)
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
			return r.a.errorf("session/load: %w", err)
		}
		res.SessionID = r.spec.ResumeID
	}
	if res.SessionID == "" {
		return r.a.errorf("no session id")
	}
	r.mu.Lock()
	r.sessionID = res.SessionID
	r.options = res.ConfigOptions
	r.banner = res.Meta.PiAcp.StartupInfo
	r.mu.Unlock()

	if r.spec.Model != "" {
		if err := r.setOption(ictx, catModel, r.spec.Model); err != nil {
			return err
		}
	}
	// Taken after the model: switching models may reset it.
	r.mu.Lock()
	if o, ok := findOption(r.options, catEffort); ok && !slices.ContainsFunc(o.Options, isEffortDefault) {
		r.baseEffort = o.CurrentValue
	}
	r.mu.Unlock()
	if r.spec.Effort != "" && r.hasOption(catEffort) {
		_ = r.setOption(ictx, catEffort, r.a.toAgentEffort(r.spec.Effort))
	}
	if r.spec.Mode == agent.ModePlan && r.modeValue(agent.ModePlan) != "" {
		_ = r.setOption(ictx, catMode, modePlan)
	}

	raw, _ := json.Marshal(map[string]any{"sessionId": res.SessionID, "resumed": r.spec.ResumeID != ""})
	e := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindSessionStarted, SessionID: res.SessionID, Text: r.current(catModel), Raw: raw}
	r.events <- e
	// ACP reports no plan windows, and getting them some other way would
	// mean reading the agent's stored credentials.
	r.events <- agent.UnsupportedLimits(r.spec.ID)
	return nil
}

func isEffortDefault(v optionValue) bool { return v.Value == effortDefault }

// findOption is the session's option for cat: the one of that category, or
// else the one with that id (OpenCode's "effort" is a thought_level).
func findOption(opts []configOption, cat string) (configOption, bool) {
	for _, o := range opts {
		if o.Category == cat {
			return o, true
		}
	}
	for _, o := range opts {
		if o.ID == cat || cat == catEffort && o.ID == "effort" {
			return o, true
		}
	}
	return configOption{}, false
}

// setOption sets the session's option for cat and keeps the options the
// answer reports.
func (r *run) setOption(ctx context.Context, cat, value string) error {
	sid := r.SessionID()
	if sid == "" {
		return r.a.errorf("no session")
	}
	o, ok := r.option(cat)
	if !ok {
		return r.a.errorf("no %s option", cat)
	}
	var res setOptionResult
	if err := r.p.client.Call(ctx, methodSessionSetOpt, setOptionParams{SessionID: sid, ConfigID: o.ID, Value: value}, &res); err != nil {
		return r.a.errorf("set %s: %w", o.ID, err)
	}
	if len(res.ConfigOptions) > 0 {
		r.mu.Lock()
		r.options = res.ConfigOptions
		r.mu.Unlock()
	}
	return nil
}

func (r *run) option(cat string) (configOption, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return findOption(r.options, cat)
}

func (r *run) hasOption(cat string) bool { _, ok := r.option(cat); return ok }

func (r *run) current(cat string) string { o, _ := r.option(cat); return o.CurrentValue }

// modeValue is the session's mode for m: "plan" for plan mode when the agent
// has one, and its first other mode (OpenCode's "build") otherwise; "" when
// the agent has no such mode.
func (r *run) modeValue(m agent.Mode) string {
	o, ok := r.option(catMode)
	if !ok {
		return ""
	}
	for _, v := range o.Options {
		if (v.Value == modePlan) == (m == agent.ModePlan) {
			return v.Value
		}
	}
	return ""
}

func (r *run) emit(e agent.Event) {
	select {
	case r.events <- e:
	case <-r.closing:
	}
}

// loop consumes everything the server sends until the process goes away.
func (r *run) loop() {
	defer close(r.loopDone)
	inTurn := false
	for m := range r.p.client.Incoming() {
		switch {
		case m.IsRequest():
			r.handleServerRequest(m)
		case m.Method == "deck/loaded":
			r.mu.Lock()
			r.loading = false
			r.mu.Unlock()
		case m.Method == turnStarted:
			inTurn = true
		case m.Method == turnEnded:
			inTurn = false
			r.endTurn(m.Params)
		case m.Method == notifySessionUpdate:
			// Decoded once, on the client's read loop (decodeUpdate).
			p, ok := m.Value.(*updateParams)
			if !ok {
				continue
			}
			r.mu.Lock()
			drop := r.loading
			if p.Update.SessionUpdate == updConfigOptionUpdate && len(p.Update.ConfigOptions) > 0 {
				r.options = p.Update.ConfigOptions
			}
			// Text outside a turn, or the banner the agent announced (it
			// may be read after the first prompt went out), is the
			// agent's own, not a reply to anyone.
			if p.Update.SessionUpdate == updAgentMessage || p.Update.SessionUpdate == updAgentThought {
				banner := r.banner != "" && p.Update.Content != nil && p.Update.Content.Text == r.banner
				if banner {
					r.banner = ""
				}
				drop = drop || banner || !inTurn
			}
			r.mu.Unlock()
			if drop {
				continue
			}
			r.trMu.Lock()
			evs := r.tr.update(p.Update)
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
	evs := r.tr.flush()
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

// mediaBlocks turns the attachments into prompt blocks: each one's
// reference line, then an image block with its bytes or, for a video, a
// resource_link to the file.
func (r *run) mediaBlocks(images []agent.Image) ([]contentBlock, error) {
	var blocks []contentBlock
	refs := agent.RefLines(images)
	for i, im := range images {
		blocks = append(blocks, contentBlock{Type: "text", Text: refs[i]})
		if im.IsVideo() {
			if r.a.cfg.Video == nil {
				return nil, r.a.errorf("video: %s takes no video", r.a.cfg.Kind)
			}
			abs, err := filepath.Abs(im.Path)
			if err != nil {
				return nil, r.a.errorf("video: %w", err)
			}
			if fi, err := os.Stat(abs); err != nil || !fi.Mode().IsRegular() {
				return nil, r.a.errorf("video: %s is not a readable file", filepath.Base(abs))
			}
			name := agent.CleanLabel(im.Label)
			if name == "" {
				name = filepath.Base(abs)
			}
			u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
			if !strings.HasPrefix(u.Path, "/") {
				u.Path = "/" + u.Path // a Windows drive path
			}
			blocks = append(blocks, contentBlock{Type: "resource_link", URI: u.String(), Name: name, MimeType: im.MediaType})
			continue
		}
		data, err := os.ReadFile(im.Path)
		if err != nil {
			return nil, r.a.errorf("image: %w", err)
		}
		blocks = append(blocks, contentBlock{Type: "image", MimeType: im.MediaType, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return blocks, nil
}

// SendWithImages starts a turn whose prompt is the images followed by the
// text, each image after a text block with its reference line ("[Image 1:
// login page error]"). A video goes as a resource_link to its file, which
// the agent reads itself, and only to an agent whose Config takes video.
// The first prompt of a process carries Deck's instructions ahead of it:
// ACP has no separate place for them.
func (r *run) SendWithImages(prompt string, images []agent.Image) error {
	sid := r.SessionID()
	if sid == "" {
		return r.a.errorf("no session")
	}
	blocks, err := r.mediaBlocks(images)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return r.a.errorf("a turn is already running")
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
	r.p.client.Inject(codex.Message{Method: turnStarted})

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

// SetMode switches the agent between its plan mode and its other one (for
// OpenCode, its plan and build agents), when it has them. Deck enforces the
// mode on approvals itself either way.
func (r *run) SetMode(m agent.Mode) error {
	v := r.modeValue(m)
	if v == "" || v == r.current(catMode) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.setOption(ctx, catMode, v)
}

// SetModel switches the session's model ("provider/model"); it applies from
// the next turn.
func (r *run) SetModel(model string) error {
	if model == "" {
		return r.a.errorf("empty model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.setOption(ctx, catModel, model)
}

// SetEffort sets the model's reasoning effort ("" = its default: the
// agent's "default" value, or the effort the session started with). A model
// without an effort setting ignores it.
func (r *run) SetEffort(effort string) error {
	o, ok := r.option(catEffort)
	if !ok {
		if effort == "" {
			return nil
		}
		return r.a.errorf("this model has no effort setting")
	}
	v := r.a.toAgentEffort(effort)
	if effort == "" {
		r.mu.Lock()
		v = r.baseEffort
		r.mu.Unlock()
		if slices.ContainsFunc(o.Options, isEffortDefault) {
			v = effortDefault
		}
		if v == "" {
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.setOption(ctx, catEffort, v)
}

// toAgentEffort is Deck's effort level in the agent's words.
func (a *Adapter) toAgentEffort(level string) string {
	for k, v := range a.cfg.EffortAliases {
		if v == level {
			return k
		}
	}
	return level
}

// fromAgentEffort is the agent's effort value in Deck's words.
func (a *Adapter) fromAgentEffort(v string) string {
	if l, ok := a.cfg.EffortAliases[v]; ok {
		return l
	}
	return v
}

var (
	_ agent.ModeSetter   = (*run)(nil)
	_ agent.ModelSetter  = (*run)(nil)
	_ agent.EffortSetter = (*run)(nil)
	_ agent.ImageSender  = (*run)(nil)
)
