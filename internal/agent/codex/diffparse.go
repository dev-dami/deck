package codex

import (
	"strconv"
	"strings"

	"github.com/dev-dami/deck/internal/agent"
)

// ParseUnified parses a unified diff (git style or plain ---/+++ style, one or
// many files) into FileDiffs, counting Added and Removed lines.
func ParseUnified(diff string) []agent.FileDiff {
	p := &diffParser{}
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	// A trailing newline yields one empty trailing element; drop it.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, l := range lines {
		next := ""
		if i+1 < len(lines) {
			next = lines[i+1]
		}
		p.line(l, next)
	}
	p.flushFile()
	return p.files
}

type diffParser struct {
	files []agent.FileDiff
	cur   *agent.FileDiff
	hunk  *agent.Hunk

	// remaining old/new lines in the current hunk; -1 means unbounded
	// (the header carried no counts).
	remOld, remNew int
	sawMinus       bool // saw a "---" header for cur
	sawPlus        bool // saw the matching "+++" header
	oldHdr, newHdr string
}

func (p *diffParser) flushHunk() {
	if p.hunk != nil && p.cur != nil {
		p.cur.Hunks = append(p.cur.Hunks, *p.hunk)
	}
	p.hunk = nil
	p.remOld, p.remNew = 0, 0
}

func (p *diffParser) flushFile() {
	p.flushHunk()
	if p.cur != nil {
		f := *p.cur
		if f.Path == "" {
			f.Path = f.OldPath
		}
		if f.Path == "" {
			f.Path = "(unknown)" // keep the counts even without a path
		}
		if f.OldPath == f.Path {
			f.OldPath = ""
		}
		p.files = append(p.files, f)
	}
	p.cur = nil
	p.sawMinus, p.sawPlus = false, false
	p.oldHdr, p.newHdr = "", ""
}

func (p *diffParser) startFile() {
	p.flushFile()
	p.cur = &agent.FileDiff{}
}

func (p *diffParser) ensureFile() {
	if p.cur == nil {
		p.cur = &agent.FileDiff{}
	}
}

func (p *diffParser) inHunk() bool {
	return p.hunk != nil && (p.remOld != 0 || p.remNew != 0)
}

func (p *diffParser) line(l, next string) {
	// Hunk body first: a body line may legitimately look like a header
	// ("--- foo" is the removed line "-- foo").
	if p.inHunk() && p.bodyLine(l, next) {
		return
	}
	switch {
	case strings.HasPrefix(l, "diff --git "):
		p.startFile()
		a, b := splitGitPaths(strings.TrimPrefix(l, "diff --git "))
		p.cur.OldPath = stripPrefix(a)
		p.cur.Path = stripPrefix(b)
	case strings.HasPrefix(l, "new file mode"):
		p.ensureFile()
		p.cur.New = true
	case strings.HasPrefix(l, "deleted file mode"):
		p.ensureFile()
		p.cur.Deleted = true
	case strings.HasPrefix(l, "rename from "):
		p.ensureFile()
		p.cur.OldPath = strings.TrimPrefix(l, "rename from ")
	case strings.HasPrefix(l, "rename to "):
		p.ensureFile()
		p.cur.Path = strings.TrimPrefix(l, "rename to ")
	case strings.HasPrefix(l, "--- ") && strings.HasPrefix(next, "+++ "):
		// A "---" after hunks or after a finished header pair starts a new
		// file; right after a "diff --git" header it belongs to that file.
		if p.cur == nil || len(p.cur.Hunks) > 0 || p.hunk != nil || p.sawMinus {
			p.startFile()
		}
		p.sawMinus = true
		p.oldHdr = headerPath(strings.TrimPrefix(l, "--- "))
	case strings.HasPrefix(l, "+++ ") && p.sawMinus && !p.sawPlus:
		p.sawPlus = true
		p.newHdr = headerPath(strings.TrimPrefix(l, "+++ "))
		p.applyHeaders()
	case strings.HasPrefix(l, "@@"):
		p.ensureFile()
		p.flushHunk()
		h, ok := parseHunkHeader(l)
		if !ok {
			return
		}
		hk := h.hunk
		p.hunk = &hk
		p.remOld, p.remNew = h.remOld, h.remNew
	}
}

func (p *diffParser) applyHeaders() {
	p.ensureFile()
	switch {
	case p.oldHdr == "/dev/null":
		p.cur.New = true
		p.cur.Path = stripPrefix(p.newHdr)
		p.cur.OldPath = ""
	case p.newHdr == "/dev/null":
		p.cur.Deleted = true
		p.cur.Path = stripPrefix(p.oldHdr)
		p.cur.OldPath = ""
	default:
		np := stripPrefix(p.newHdr)
		op := stripPrefix(p.oldHdr)
		if p.cur.Path == "" {
			p.cur.Path = np
		}
		if op != np && p.cur.OldPath == "" {
			p.cur.OldPath = op
		}
	}
}

// bodyLine consumes a hunk body line. It returns false when the line is not
// part of the hunk (so the caller treats it as a header).
func (p *diffParser) bodyLine(l, next string) bool {
	if strings.HasPrefix(l, "\\") { // "\ No newline at end of file"
		return true
	}
	if p.remOld < 0 || p.remNew < 0 {
		// Without counts we must not swallow the next file header.
		if strings.HasPrefix(l, "--- ") && strings.HasPrefix(next, "+++ ") {
			return false
		}
		if strings.HasPrefix(l, "diff --git ") || strings.HasPrefix(l, "@@") {
			return false
		}
	}
	var op agent.LineOp
	text := ""
	switch {
	case l == "":
		op = agent.OpContext
	case l[0] == ' ':
		op, text = agent.OpContext, l[1:]
	case l[0] == '+':
		op, text = agent.OpAdd, l[1:]
	case l[0] == '-':
		op, text = agent.OpDel, l[1:]
	default:
		return false
	}
	p.hunk.Lines = append(p.hunk.Lines, agent.DiffLine{Op: op, Text: text})
	switch op {
	case agent.OpAdd:
		p.cur.Added++
		if p.remNew > 0 {
			p.remNew--
		}
	case agent.OpDel:
		p.cur.Removed++
		if p.remOld > 0 {
			p.remOld--
		}
	default:
		if p.remOld > 0 {
			p.remOld--
		}
		if p.remNew > 0 {
			p.remNew--
		}
	}
	return true
}

type hunkHeader struct {
	hunk           agent.Hunk
	remOld, remNew int
}

// parseHunkHeader parses "@@ -a,b +c,d @@ optional section". A header with no
// numeric ranges yields an unbounded hunk (remOld/remNew = -1).
func parseHunkHeader(l string) (hunkHeader, bool) {
	var h hunkHeader
	rest := strings.TrimPrefix(l, "@@")
	end := strings.Index(rest, "@@")
	if end < 0 {
		end = len(rest) // bare "@@": no ranges, unbounded hunk
	}
	fields := strings.Fields(rest[:end])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		h.remOld, h.remNew = -1, -1
		return h, true
	}
	os, ol, ok1 := parseRange(fields[0][1:])
	ns, nl, ok2 := parseRange(fields[1][1:])
	if !ok1 || !ok2 {
		h.remOld, h.remNew = -1, -1
		return h, true
	}
	h.hunk = agent.Hunk{OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl}
	h.remOld, h.remNew = ol, nl
	return h, true
}

func parseRange(s string) (start, n int, ok bool) {
	a, b, hasComma := strings.Cut(s, ",")
	var err error
	if start, err = strconv.Atoi(a); err != nil {
		return 0, 0, false
	}
	if !hasComma {
		return start, 1, true
	}
	if n, err = strconv.Atoi(b); err != nil {
		return 0, 0, false
	}
	return start, n, true
}

func splitGitPaths(rest string) (a, b string) {
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	f := strings.Fields(rest)
	switch len(f) {
	case 0:
		return "", ""
	case 1:
		return f[0], f[0]
	}
	return f[0], f[len(f)-1]
}

func stripPrefix(p string) string {
	p = strings.Trim(p, "\"")
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// headerPath trims a trailing tab+timestamp from a ---/+++ line.
func headerPath(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
