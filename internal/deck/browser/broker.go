// Package browser brokers live commands between the engine and its local
// desktop. Commands never enter the replayable event feed or the thread log.
package browser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"golang.org/x/net/idna"
	"google.golang.org/protobuf/proto"
)

const ExecutionTimeout = 30 * time.Second

var ErrController = errors.New("invalid or disconnected desktop browser controller")

type pending struct {
	request   *pb.BrowserRequest
	result    chan *pb.BrowserActionResponse
	timer     *time.Timer
	ctx       context.Context
	cancel    context.CancelFunc
	approving bool
	deadline  time.Time
}

type Broker struct {
	mu         sync.Mutex
	capability string
	controller *pb.BrowserController
	live       chan *pb.BrowserRequest
	done       chan struct{}
	attached   bool
	pending    map[string]*pending
	states     map[string]*pb.BrowserStateResponse
	grants     map[string]map[string]bool
	approve    func(context.Context, string, string, string) bool
	changed    func(*pb.BrowserStateResponse)
	execution  time.Duration
	lifecycle  context.Context
	stop       context.CancelFunc
}

func New(capability string, approve func(context.Context, string, string, string) bool, changed func(*pb.BrowserStateResponse)) *Broker {
	return &Broker{capability: capability, pending: map[string]*pending{}, states: map[string]*pb.BrowserStateResponse{}, grants: map[string]map[string]bool{}, approve: approve, changed: changed, execution: ExecutionTimeout}
}
func id() string                   { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func clone[T proto.Message](m T) T { return proto.Clone(m).(T) }
func result(code, text string) *pb.BrowserActionResponse {
	return &pb.BrowserActionResponse{ErrorCode: code, Result: &pb.OpResult{Text: text, IsError: code != ""}}
}

func (b *Broker) valid(r *pb.BrowserControllerRef) bool {
	return r != nil && b.controller != nil && r.Id == b.controller.Id && r.Generation == b.controller.Generation && subtle.ConstantTimeCompare([]byte(r.DesktopCapability), []byte(b.capability)) == 1
}

func (b *Broker) Register(capability, session string) (*pb.BrowserController, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if session != "" || capability == "" || subtle.ConstantTimeCompare([]byte(capability), []byte(b.capability)) != 1 {
		return nil, ErrController
	}
	b.disconnect()
	b.controller = &pb.BrowserController{Id: id(), Generation: id()}
	b.lifecycle, b.stop = context.WithCancel(context.Background())
	b.live = make(chan *pb.BrowserRequest, 64)
	b.done = make(chan struct{})
	return clone(b.controller), nil
}

// Attach establishes the sole non-replayable stream. A registration alone
// cannot receive commands and is not reported as available.
func (b *Broker) Attach(r *pb.BrowserControllerRef) (<-chan *pb.BrowserRequest, <-chan struct{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(r) || b.attached {
		return nil, nil, ErrController
	}
	b.attached = true
	return b.live, b.done, nil
}
func (b *Broker) Disconnect(r *pb.BrowserControllerRef) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(r) {
		return ErrController
	}
	b.disconnect()
	return nil
}
func (b *Broker) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.disconnect() }
func (b *Broker) disconnect() {
	if b.stop != nil {
		b.stop()
		b.stop = nil
	}
	if b.done != nil {
		close(b.done)
		b.done = nil
	}
	for _, p := range b.pending {
		p.cancel()
		select {
		case p.result <- result("browser_unavailable", "The desktop browser disconnected."):
		default:
		}
	}
	b.pending = map[string]*pending{}
	b.controller = nil
	b.attached = false
	b.grants = map[string]map[string]bool{}
	for sid := range b.states {
		delete(b.states, sid)
		b.emit(&pb.BrowserStateResponse{SessionId: sid})
	}
}
func (b *Broker) emit(s *pb.BrowserStateResponse) {
	if b.changed != nil {
		b.changed(clone(s))
	}
}
func (b *Broker) State(sid string) *pb.BrowserStateResponse {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.states[sid]; s != nil {
		out := clone(s)
		out.Available = b.attached
		return out
	}
	return &pb.BrowserStateResponse{SessionId: sid, Available: b.attached}
}
func (b *Broker) Update(r *pb.BrowserControllerRef, s *pb.BrowserStateResponse) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(r) || !b.attached {
		return ErrController
	}
	if s == nil || s.SessionId == "" || len(s.Tabs) > 16 {
		return errors.New("invalid browser state")
	}
	seen := map[string]bool{}
	for _, t := range s.Tabs {
		if t == nil || t.Id == "" || t.SessionId != s.SessionId || seen[t.Id] {
			return errors.New("invalid browser tab")
		}
		seen[t.Id] = true
	}
	s = clone(s)
	s.Available = true
	b.states[s.SessionId] = s
	b.emit(s)
	return nil
}
func (b *Broker) Complete(r *pb.BrowserControllerRef, request string, response *pb.BrowserActionResponse) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(r) || !b.attached {
		return ErrController
	}
	p := b.pending[request]
	if p == nil || response == nil {
		return errors.New("unknown or expired browser request")
	}
	if p.ctx.Err() != nil || (!p.approving && !p.deadline.IsZero() && !time.Now().Before(p.deadline)) {
		return errors.New("expired browser request")
	}
	if response.Tab != nil && response.Tab.SessionId != p.request.SessionId {
		return errors.New("browser result belongs to another room")
	}
	if response.Result != nil {
		images := make([]agent.OpImage, 0, len(response.Result.Images))
		for _, im := range response.Result.Images {
			images = append(images, agent.OpImage{MimeType: im.MimeType, Data: im.Data})
		}
		if err := (agent.OpResult{Images: images}).ValidateImages(); err != nil {
			response = result("unsupported_image", err.Error())
		}
		if len(response.Result.Text) > 64<<10 {
			return errors.New("browser result too large")
		}
	}
	select {
	case p.result <- clone(response):
	default:
		return errors.New("duplicate browser result")
	}
	delete(b.pending, request)
	return nil
}

// Origin returns a normalized http(s) origin, rejecting URL credentials and
// non-web schemes. Loopback needs no stored grant.
func Origin(raw string) (origin string, local bool, err error) {
	if len(raw) > 8192 {
		return "", false, errors.New("URL too long")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, errors.New("only http(s) URLs without credentials are allowed")
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) == nil {
		var err error
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return "", false, errors.New("invalid URL hostname")
		}
	}
	ip := net.ParseIP(host)
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", false, errors.New("invalid URL port")
		}
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port != "" {
		authority = net.JoinHostPort(host, port)
	}
	return u.Scheme + "://" + authority, host == "localhost" || (ip != nil && ip.IsLoopback()), nil
}
func (b *Broker) grant(ctx context.Context, sid, owner, origin string) bool {
	b.mu.Lock()
	allowed := b.grants[sid][origin]
	generation := ""
	if b.controller != nil {
		generation = b.controller.Generation
	}
	b.mu.Unlock()
	if allowed {
		return true
	}
	if b.approve == nil || !b.approve(ctx, sid, owner, origin) || ctx.Err() != nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached || b.controller == nil || b.controller.Generation != generation {
		return false
	}
	if b.grants[sid] == nil {
		b.grants[sid] = map[string]bool{}
	}
	b.grants[sid][origin] = true
	return true
}
func (b *Broker) ApproveOrigin(ctx context.Context, r *pb.BrowserOriginRequest) (bool, error) {
	origin, local, err := Origin(r.Origin)
	if err != nil || origin != r.Origin {
		return false, errors.New("invalid origin")
	}
	b.mu.Lock()
	if !b.valid(r.Controller) || !b.attached {
		b.mu.Unlock()
		return false, ErrController
	}
	owner := ""
	waitCtx := ctx
	lifecycle := b.lifecycle
	p := b.pending[r.RequestId]
	if r.RequestId != "" {
		if p == nil || p.request.SessionId != r.SessionId || p.approving {
			b.mu.Unlock()
			return false, errors.New("unknown origin request")
		}
		if !p.deadline.IsZero() && !time.Now().Before(p.deadline) {
			b.mu.Unlock()
			return false, errors.New("expired browser request")
		}
		p.approving = true
		p.timer.Stop()
		owner = p.request.Owner
		waitCtx = p.ctx
	} else {
		var tab *pb.BrowserTab
		if state := b.states[r.SessionId]; state != nil {
			for _, t := range state.Tabs {
				if t.Id == r.TabId {
					tab = t
					break
				}
			}
		}
		if tab == nil {
			b.mu.Unlock()
			return false, errors.New("unknown browser tab")
		}
		if !tab.UserInControl {
			owner = tab.Owner
		}
	}
	b.mu.Unlock()
	waitCtx, cancel := context.WithCancel(waitCtx)
	defer cancel()
	stop := context.AfterFunc(lifecycle, cancel)
	defer stop()
	allowed := local || b.grant(waitCtx, r.SessionId, owner, origin)
	if p != nil {
		b.mu.Lock()
		p.approving = false
		if b.pending[r.RequestId] == p {
			p.deadline = time.Now().Add(b.execution)
			p.timer.Reset(b.execution)
		}
		b.mu.Unlock()
	}
	return allowed, nil
}

func (b *Broker) Action(ctx context.Context, sid, owner string, c *pb.BrowserCommand) *pb.BrowserActionResponse {
	if c == nil || sid == "" {
		return result("invalid_request", "A browser command and room are required.")
	}
	c = clone(c)
	if !validCommand(c, owner) {
		return result("invalid_request", "Invalid browser command.")
	}
	b.mu.Lock()
	if !b.attached {
		b.mu.Unlock()
		return result("browser_unavailable", "No local desktop browser is attached.")
	}
	if c.Action == "tabs" {
		state := &pb.BrowserStateResponse{SessionId: sid, Available: true}
		if current := b.states[sid]; current != nil {
			for _, tab := range current.Tabs {
				if owner != "" && tab.UserInControl {
					state.Tabs = append(state.Tabs, &pb.BrowserTab{Id: tab.Id, SessionId: sid, Owner: tab.Owner, UserInControl: true})
				} else {
					state.Tabs = append(state.Tabs, clone(tab))
				}
			}
		}
		b.mu.Unlock()
		data, _ := json.Marshal(state)
		return result("", "UNTRUSTED BROWSER DATA:\n"+string(data))
	}
	// User takeover preempts pending reads, inputs and approval waits.
	if owner == "" && (c.Action == "take_control" || c.Action == "navigate" || c.Action == "back" || c.Action == "forward" || c.Action == "reload" || c.Action == "close") {
		for key, p := range b.pending {
			if p.request.SessionId == sid && p.request.Command.TabId == c.TabId {
				p.cancel()
				select {
				case p.result <- result("cancelled", "The user took control."):
				default:
				}
				b.send(&pb.BrowserRequest{Id: key, Cancelled: true})
				delete(b.pending, key)
			}
		}
	}
	if c.Action != "open" {
		var tab *pb.BrowserTab
		if s := b.states[sid]; s != nil {
			for _, t := range s.Tabs {
				if t.Id == c.TabId {
					tab = t
					break
				}
			}
		}
		if tab == nil {
			b.mu.Unlock()
			return result("invalid_request", "Unknown tab in this room.")
		}
		if owner != "" && tab.UserInControl {
			b.mu.Unlock()
			return result("user_in_control", "The user controls this tab.")
		}
		if owner != "" && owner != tab.Owner && c.Action != "snapshot" && c.Action != "screenshot" {
			b.mu.Unlock()
			return result("not_owner", "Another agent owns this tab.")
		}
		if owner == "" && (c.Action == "take_control" || c.Action == "navigate" || c.Action == "back" || c.Action == "forward" || c.Action == "reload") {
			tab.UserInControl = true
			b.emit(b.states[sid])
		}
	}
	generation := b.controller.Generation
	request := &pb.BrowserRequest{Id: id(), SessionId: sid, Owner: owner, Command: c, ControllerId: b.controller.Id, ControllerGeneration: generation}
	pctx, cancel := context.WithCancel(ctx)
	p := &pending{request: request, result: make(chan *pb.BrowserActionResponse, 1), timer: time.NewTimer(time.Hour), ctx: pctx, cancel: cancel, approving: true}
	p.timer.Stop()
	b.pending[request.Id] = p
	b.mu.Unlock()
	defer func() {
		cancel()
		p.timer.Stop()
		b.mu.Lock()
		if b.pending[request.Id] == p {
			delete(b.pending, request.Id)
			b.send(&pb.BrowserRequest{Id: request.Id, Cancelled: true})
		}
		b.mu.Unlock()
	}()
	if c.Action == "open" || c.Action == "navigate" {
		origin, local, err := Origin(c.Url)
		if err != nil {
			return result("origin_denied", err.Error())
		}
		if !local && !b.grant(pctx, sid, owner, origin) {
			select {
			case response := <-p.result:
				return response
			default:
			}
			if pctx.Err() != nil {
				return result("cancelled", "The browser approval was cancelled.")
			}
			return result("origin_denied", "The user did not approve this origin.")
		}
	}
	b.mu.Lock()
	if !b.attached || b.controller == nil || b.controller.Generation != generation || b.pending[request.Id] != p || pctx.Err() != nil {
		b.mu.Unlock()
		return result("browser_unavailable", "The desktop browser disconnected.")
	}
	request.DeadlineAt = time.Now().Add(b.execution).UnixMilli()
	p.deadline = time.UnixMilli(request.DeadlineAt)
	p.approving = false
	p.timer.Reset(b.execution)
	sent := b.send(request)
	b.mu.Unlock()
	if !sent {
		return result("browser_unavailable", "The browser controller is not receiving commands.")
	}
	for {
		select {
		case out := <-p.result:
			return out
		case <-pctx.Done():
			select {
			case out := <-p.result:
				return out
			default:
			}
			return result("cancelled", "The browser command was cancelled.")
		case <-p.timer.C:
			b.mu.Lock()
			approving := p.approving
			remaining := time.Until(p.deadline)
			if !approving && remaining > 0 {
				p.timer.Reset(remaining)
			}
			b.mu.Unlock()
			if approving || remaining > 0 {
				continue
			}
			return result("timeout", "The browser command timed out.")
		}
	}
}
func (b *Broker) send(r *pb.BrowserRequest) bool {
	if !b.attached {
		return false
	}
	if b.controller != nil {
		r.ControllerId = b.controller.Id
		r.ControllerGeneration = b.controller.Generation
	}
	select {
	case b.live <- r:
		return true
	default:
		return false
	}
}
func validCommand(c *pb.BrowserCommand, owner string) bool {
	if len(c.Text) > 16384 || len(c.TabId) > 256 || len(c.Url) > 8192 {
		return false
	}
	if c.Action == "open" {
		return c.Url != ""
	}
	if c.Action == "tabs" {
		return c.TabId == ""
	}
	if c.TabId == "" {
		return false
	}
	switch c.Action {
	case "navigate":
		return c.Url != ""
	case "back", "forward", "reload", "snapshot", "screenshot", "close":
		return true
	case "click", "type":
		return c.ElementRef != "" && c.SnapshotVersion != ""
	case "handoff":
		return c.TargetAgent != "" && len(c.TargetAgent) <= 128
	case "take_control", "release_control":
		return owner == ""
	}
	return false
}
