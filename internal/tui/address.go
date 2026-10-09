package tui

import (
	"strings"

	"github.com/dev-dami/deck/internal/session"
)

// Commands are the five slash commands, in menu order.
var Commands = []struct{ Name, Args, Desc string }{
	{"new", "", "start a fresh thread"},
	{"resume", "", "continue a saved thread"},
	{"model", "<name>", "pick a model, or name one"},
	{"cd", "<path>", "directory for new threads"},
	{"name", "<name>", "your display name"},
	{"changes", "", "what changed since your messages, with undo"},
}

func isCommand(name string) bool {
	for _, c := range Commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

// parseCommand reads a submitted line that starts with "/".
func parseCommand(text string) (name, arg string, ok bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") {
		return "", "", false
	}
	head, rest := splitToken(t)
	return strings.TrimPrefix(head, "/"), rest, true
}

// splitToken splits "head rest" at the first whitespace; rest is trimmed.
func splitToken(s string) (head, rest string) {
	i := strings.IndexAny(s, " \t\r\n")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

// refsOf lists a thread's members the way session.ParseAddressing wants them.
func refsOf(th session.ThreadInfo) []session.MemberRef {
	refs := make([]session.MemberRef, 0, len(th.Members))
	for _, a := range th.Members {
		refs = append(refs, session.MemberRef{ID: a.ID, Name: a.Name, Kind: a.Kind, Exited: a.Status == session.StatusExited})
	}
	return refs
}

// addressHint says, while you type, where Post would send the text:
//
//	→ codex            addressed to that member
//	→ all              @all
//	→ claude (last spoke)  no mention, goes to whoever spoke last
//	+ adds codex       @codex names a kind with no live member in the thread
//
// It uses session.ParseAddressing, so it agrees with what Post will do. A
// thread with at most one member has nothing to say unless a new agent is named.
func addressHint(text string, th session.ThreadInfo, kinds []string) string {
	t := strings.TrimSpace(text)
	if t == "" || strings.HasPrefix(t, "/") {
		return ""
	}
	ad := session.ParseAddressing(t, refsOf(th), kinds)
	if len(ad.Unknown) > 0 {
		return "no agent @" + ad.Unknown[0]
	}
	find := func(id string) session.AgentInfo {
		for _, a := range th.Members {
			if a.ID == id {
				return a
			}
		}
		return session.AgentInfo{ID: id, Name: id}
	}
	for _, id := range ad.Members {
		if a := find(id); a.Status == session.StatusExited {
			return a.Name + " has exited"
		}
	}
	var parts []string
	switch {
	case ad.All:
		parts = append(parts, "→ all")
	case len(ad.Members) > 0:
		names := make([]string, len(ad.Members))
		for i, id := range ad.Members {
			names[i] = find(id).Name
		}
		parts = append(parts, "→ "+strings.Join(names, ", "))
	}
	if len(ad.NewKinds) > 0 {
		parts = append(parts, "+ adds "+strings.Join(ad.NewKinds, ", "))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " "+glyphDot+" ")
	}
	if len(th.Members) < 2 {
		return ""
	}
	for _, a := range th.Members {
		if a.ID == th.LastSpeaker && a.Status != session.StatusExited {
			return "→ " + a.Name + " (last spoke)"
		}
	}
	for _, a := range th.Members {
		if a.Status != session.StatusExited {
			return "→ " + a.Name
		}
	}
	return ""
}

// hintIsWarning reports whether an addressing hint says something is wrong.
func hintIsWarning(h string) bool {
	return strings.HasPrefix(h, "no agent") || strings.HasSuffix(h, "has exited")
}
