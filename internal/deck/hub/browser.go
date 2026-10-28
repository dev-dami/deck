package hub

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// The browser ops. The browser is the desktop app's: one Chromium and one
// session for every agent, each agent working in tabs it owns. An op here is
// checked, then handed to [Env.Browser], which the session package
// implements (permissions, then the channel to the desktop app).
//
// Agents see a page as a snapshot: a compact outline in which each element
// that can be acted on has a ref like e12. The ref is what click, type,
// select and scroll take. A ref stays valid until the page changes under
// it; the op then fails and says to snapshot again.
const (
	OpBrowserOpen       = "browser.open"
	OpBrowserTabs       = "browser.tabs"
	OpBrowserSnapshot   = "browser.snapshot"
	OpBrowserClick      = "browser.click"
	OpBrowserType       = "browser.type"
	OpBrowserPress      = "browser.press"
	OpBrowserScroll     = "browser.scroll"
	OpBrowserSelect     = "browser.select"
	OpBrowserWait       = "browser.wait"
	OpBrowserBack       = "browser.back"
	OpBrowserForward    = "browser.forward"
	OpBrowserReload     = "browser.reload"
	OpBrowserEval       = "browser.eval"
	OpBrowserScreenshot = "browser.screenshot"
	OpBrowserClose      = "browser.close"
)

// Limits of the browser ops' arguments.
const (
	maxBrowserText = 8000 // browser.type text, browser.eval js
	maxBrowserKey  = 40
	maxWaitMS      = 10000
	maxWaitSec     = 40
	maxScroll      = 50000
	maxSnapLines   = 2000
)

// Browser is the shared browser as one agent's browser.* ops see it: op is
// one of the OpBrowser* names and a the op's checked arguments. The text it
// returns is the op's result.
type Browser interface {
	BrowserOp(ctx context.Context, op string, a map[string]any) (string, error)
}

var (
	refRe = regexp.MustCompile(`^e[0-9]{1,6}$`)
	tabRe = regexp.MustCompile(`^t[0-9]{1,6}$`)
	// keyRe is a key name or a combination: "Enter", "Control+A", "a".
	keyRe = regexp.MustCompile(`^(?:[A-Za-z]{3,7}\+){0,3}(?:[A-Za-z][A-Za-z0-9]{0,15}|[^\s])$`)
)

var tabArg = Arg{Name: "tab", Type: tString, Desc: "a tab id from browser.tabs (default: your latest tab)"}

func browserOps() []Op {
	b := func(name, desc string, args ...Arg) Op {
		return Op{Name: name, Desc: desc, Args: args, run: opBrowser(name)}
	}
	return []Op{
		b(OpBrowserOpen, "Open a web page (http or https) in your own tab of the shared browser, which the user can watch and take over. Pages of other sites than those you opened before may need the user's approval. Returns the tab id; call browser.snapshot to see the page.",
			Arg{Name: "url", Type: tString, Required: true}, Arg{Name: "new_tab", Type: tBool, Desc: "open a further tab instead of reusing your latest"}, tabArg),
		b(OpBrowserTabs, "List the tabs of the shared browser: id, owner, address and title. You can act only on your own."),
		b(OpBrowserSnapshot, "See the page as a compact outline of text. Each element you can act on has a ref like e12 (button, link, textbox, ...), which browser.click, type, select and scroll take. Prefer this to a screenshot.",
			tabArg, Arg{Name: "interactive", Type: tBool, Desc: "list only the elements with refs"}, Arg{Name: "max", Type: tInt, Desc: "most lines to return (default 250)"}),
		b(OpBrowserClick, "Click the element with this ref (from the latest browser.snapshot). Returns what changed: the page's address and title.",
			Arg{Name: "ref", Type: tString, Required: true}, tabArg, Arg{Name: "double", Type: tBool}),
		b(OpBrowserType, "Put text in the field with this ref (replacing its content unless append), then press Enter when submit is set. The text is never logged by Deck.",
			Arg{Name: "ref", Type: tString, Required: true}, Arg{Name: "text", Type: tString, Required: true}, Arg{Name: "submit", Type: tBool},
			Arg{Name: "append", Type: tBool}, tabArg),
		b(OpBrowserPress, "Press a key on the page's focused element: Enter, Tab, Escape, ArrowDown, Backspace, PageDown, or a combination like Control+A.",
			Arg{Name: "key", Type: tString, Required: true}, tabArg),
		b(OpBrowserScroll, "Scroll the page: dy pixels down (negative: up), or to \"top\" / \"bottom\", or an element into view with ref. Returns the scroll position.",
			Arg{Name: "dy", Type: tInt}, Arg{Name: "to", Type: tString, Desc: "top or bottom"}, Arg{Name: "ref", Type: tString}, tabArg),
		b(OpBrowserSelect, "Choose an option of a select element (ref) by its label or value.",
			Arg{Name: "ref", Type: tString, Required: true}, Arg{Name: "value", Type: tString, Required: true}, tabArg),
		b(OpBrowserWait, "Wait until the page shows text, until it finished loading (load), or ms milliseconds (at most 10000). timeout is in seconds (default 10).",
			Arg{Name: "text", Type: tString}, Arg{Name: "load", Type: tBool}, Arg{Name: "ms", Type: tInt}, Arg{Name: "timeout", Type: tInt}, tabArg),
		b(OpBrowserBack, "Go back in the tab's history.", tabArg),
		b(OpBrowserForward, "Go forward in the tab's history.", tabArg),
		b(OpBrowserReload, "Reload the tab.", tabArg),
		b(OpBrowserEval, "Run JavaScript in the page and return its value as JSON. The user is asked first unless the thread is in yolo mode. For things a snapshot does not show.",
			Arg{Name: "js", Type: tString, Required: true, Desc: "an expression or async function body"}, tabArg),
		b(OpBrowserScreenshot, "Take a downscaled screenshot of the tab and return the path of the image file (read it as an image). Costs far more than browser.snapshot; use it only for what text cannot tell.",
			tabArg, Arg{Name: "ref", Type: tString, Desc: "an element to capture instead of the viewport"}),
		b(OpBrowserClose, "Close one of your tabs.", tabArg),
	}
}

// opBrowser is the runner of the browser op name: it checks the arguments,
// then hands the op to the shared browser.
func opBrowser(name string) func(context.Context, *Env, args) (string, error) {
	return func(ctx context.Context, env *Env, a args) (string, error) {
		if err := checkBrowser(name, a); err != nil {
			return "", err
		}
		if env.Browser == nil {
			return "", errors.New("the browser is not available here")
		}
		return env.Browser.BrowserOp(ctx, name, map[string]any(a))
	}
}

// checkBrowser validates the arguments of a browser op beyond their types.
func checkBrowser(name string, a args) error {
	if t := a.str("tab"); t != "" && !tabRe.MatchString(t) {
		return fmt.Errorf("tab %q is not a tab id (like t3; browser.tabs lists them)", clipArg(t))
	}
	if r := a.str("ref"); r != "" && !refRe.MatchString(r) {
		return fmt.Errorf("ref %q is not an element ref (like e12; browser.snapshot lists them)", clipArg(r))
	}
	switch name {
	case OpBrowserType:
		if n := utf8.RuneCountInString(a.str("text")); n > maxBrowserText {
			return fmt.Errorf("text is %d characters; at most %d", n, maxBrowserText)
		}
	case OpBrowserEval:
		if n := utf8.RuneCountInString(a.str("js")); n > maxBrowserText {
			return fmt.Errorf("js is %d characters; at most %d", n, maxBrowserText)
		}
	case OpBrowserPress:
		k := a.str("key")
		if len(k) > maxBrowserKey || !keyRe.MatchString(k) {
			return fmt.Errorf("key %q is not a key (Enter, Tab, ArrowDown, Control+A, ...)", clipArg(k))
		}
	case OpBrowserScroll:
		to := a.str("to")
		switch {
		case to != "" && to != "top" && to != "bottom":
			return errors.New(`to must be "top" or "bottom" (or pass ref)`)
		case to == "" && a.str("ref") == "":
			if _, ok := a["dy"]; !ok {
				return errors.New("scroll needs dy, to or ref")
			}
		}
		if dy, ok := a["dy"].(int); ok && (dy > maxScroll || dy < -maxScroll) {
			return fmt.Errorf("dy must be within %d pixels", maxScroll)
		}
	case OpBrowserWait:
		_, hasMS := a["ms"]
		if a.str("text") == "" && !a.flag("load") && !hasMS {
			return errors.New("wait needs text, load or ms")
		}
		if ms, ok := a["ms"].(int); ok && (ms < 0 || ms > maxWaitMS) {
			return fmt.Errorf("ms must be between 0 and %d", maxWaitMS)
		}
		if s, ok := a["timeout"].(int); ok && (s < 1 || s > maxWaitSec) {
			return fmt.Errorf("timeout must be between 1 and %d seconds", maxWaitSec)
		}
	case OpBrowserSnapshot:
		if n, ok := a["max"].(int); ok && (n < 10 || n > maxSnapLines) {
			return fmt.Errorf("max must be between 10 and %d lines", maxSnapLines)
		}
	case OpBrowserOpen:
		// The address itself is checked by the session, which also decides
		// whether the user must approve its site.
		if len(a.str("url")) > 2048 {
			return errors.New("url is too long")
		}
	}
	return nil
}

func clipArg(s string) string {
	if utf8.RuneCountInString(s) <= 24 {
		return s
	}
	return string([]rune(s)[:24]) + "…"
}
