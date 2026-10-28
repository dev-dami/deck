package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/browser"
	"github.com/dev-dami/deck/internal/deck/hub"
)

// The shared browser as agents use it (the browser.* ops). The browser is
// the desktop app's: one Chromium for every agent of every thread, each
// agent working in tabs it owns. The ops reach it through the broker
// (Manager.Browser), which the app's window attaches to. What an agent may
// do follows the thread's permission mode:
//
//   - yolo: any http(s) page, no questions.
//   - ask: this computer's own pages (a dev server) freely; the first page
//     of any other site asks the person, and so does every browser.eval.
//   - plan: reading only. No click, type, press, select or eval.
//
// file:, chrome:, devtools: and every other scheme are refused in every mode.
// What an agent types is passed on and never logged here.

// Browser is the channel to the desktop app's browser. The app's window
// attaches to it over gRPC (pkg/rpc), and the browser.* ops run through it.
func (m *Manager) Browser() *browser.Broker { return m.browser }

// browserBoard is the shared browser as agent a's browser.* ops see it.
type browserBoard struct {
	m *Manager
	a *agentState
}

var _ hub.Browser = browserBoard{}

// browserWrites are the ops that act on a page and can change things out in
// the world; plan mode refuses them.
var browserWrites = map[string]bool{"click": true, "type": true, "press": true, "select": true, "eval": true}

// browserMaxAsk is how long a question to the person about the browser may
// wait before the op gives up.
const browserMaxAsk = 45 * time.Second

func (b browserBoard) BrowserOp(ctx context.Context, op string, a map[string]any) (string, error) {
	name := strings.TrimPrefix(op, "browser.")
	t := b.a.thread
	mode := t.getMode()
	if mode == agent.ModePlan && browserWrites[name] {
		return "", fmt.Errorf("%s: plan mode lets you read pages, not act on them (the user can switch the mode)", op)
	}
	if hosted, _ := b.m.browser.Hosted(); !hosted {
		return "", browser.ErrNoBrowser
	}
	switch name {
	case "open":
		u, err := browser.CheckURL(fmt.Sprint(a["url"]))
		if err != nil {
			return "", err
		}
		a["url"] = u.String()
		if err := b.allowSite(ctx, u, mode); err != nil {
			return "", err
		}
	case "eval":
		if err := b.m.ask(ctx, b.a, agent.ApproveOther, op, "run JavaScript in the browser: "+clip(fmt.Sprint(a["js"]), 200), t.dir); err != nil {
			return "", fmt.Errorf("browser.eval: %w", err)
		}
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	req := browser.Request{
		Agent: b.a.id, Name: b.a.getName(), Kind: b.a.kind, Op: name, Args: raw,
		Free: mode == agent.ModeYolo, Allowed: b.allowed(), Timeout: browserTimeout(ctx, name, a),
	}
	res, err := b.m.browser.Do(ctx, req)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", errors.New(res.Text)
	}
	text := res.Text
	if len(res.Image) > 0 {
		path, err := browser.SaveShot(browser.ShotDir(), b.a.id, res.Image, res.Mime)
		if err != nil {
			return "", fmt.Errorf("the screenshot could not be saved: %v", err)
		}
		text += "\nscreenshot: " + path
	}
	return text, nil
}

// allowSite lets agent a open u: this computer's own pages and sites the
// person approved before need no question; yolo asks nothing; otherwise the
// person is asked, and a yes holds for the thread's other agents too.
func (b browserBoard) allowSite(ctx context.Context, u *url.URL, mode agent.Mode) error {
	t := b.a.thread
	origin := browser.Origin(u)
	t.mu.Lock()
	ok := t.browserOK[origin]
	t.mu.Unlock()
	if ok || mode == agent.ModeYolo || browser.Loopback(u) {
		return nil
	}
	actx, cancel := context.WithTimeout(ctx, browserMaxAsk+askMargin)
	defer cancel()
	if err := b.m.ask(actx, b.a, agent.ApproveOther, "browser.open", "open "+origin+" in the browser", t.dir); err != nil {
		return fmt.Errorf("browser.open %s: %w", origin, err)
	}
	t.mu.Lock()
	if t.browserOK == nil {
		t.browserOK = map[string]bool{}
	}
	t.browserOK[origin] = true
	t.mu.Unlock()
	return nil
}

// allowed are the sites the person let this thread's agents open.
func (b browserBoard) allowed() []string {
	t := b.a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.browserOK))
	for o := range t.browserOK {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// browserTimeout is how long the browser has for op: its own limit, within
// what is left of the call.
func browserTimeout(ctx context.Context, op string, a map[string]any) time.Duration {
	d := browser.DefaultTimeout
	if op == "wait" {
		sec := 10
		if s, ok := a["timeout"].(int); ok {
			sec = s
		} else if ms, ok := a["ms"].(int); ok {
			sec = ms/1000 + 1
		}
		d = time.Duration(sec+5) * time.Second
	}
	if dl, ok := ctx.Deadline(); ok {
		d = min(d, time.Until(dl)-time.Second)
	}
	return max(d, time.Second)
}
