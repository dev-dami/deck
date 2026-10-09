package tui

import (
	"context"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

// @file mentions: typing "@" followed by part of a path, anywhere in a
// message, lists the files of the thread's folder; picking one inserts
// "@path". The list comes from git (tracked and untracked, not ignored) or,
// outside git, a walk of the folder; it is read once per folder.

const (
	maxFiles     = 20000
	maxFileItems = maxMenuRows
)

// skipDirs are never walked outside git.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true, ".cache": true,
}

// filesMsg is a folder's file list.
type filesMsg struct {
	dir  string
	list []string
}

// filesDir is the folder @file lists: the selected thread's, or where new
// threads start.
func (m *model) filesDir() string {
	if th, ok := m.selThread(); ok && th.Dir != "" {
		return th.Dir
	}
	return m.newDir
}

// fileItems are the menu rows for query, or nil while the list loads.
func (m *model) fileItems(query string) []menuItem {
	if query == "" {
		return nil
	}
	list, ok := m.files[m.filesDir()]
	if !ok {
		m.wantFiles = m.filesDir()
		return nil
	}
	var out []menuItem
	for _, mt := range fuzzy.Find(query, list) {
		out = append(out, menuItem{label: list[mt.Index], glyph: glyphDot, gStyle: sFaint, path: mentionPath(list[mt.Index]), idx: mt.MatchedIndexes})
		if len(out) == maxFileItems {
			break
		}
	}
	return out
}

// mentionPath is how a file is written after "@": a bare top-level name with no
// extension gets "./" so it never reads as an agent's name.
func mentionPath(p string) string {
	if !strings.ContainsAny(p, "/.") {
		return "./" + p
	}
	return p
}

// filesCmd starts reading a folder's file list when the menu asked for one.
func (m *model) filesCmd() tea.Cmd {
	dir := m.wantFiles
	m.wantFiles = ""
	if dir == "" || m.filesLoading[dir] {
		return nil
	}
	if _, ok := m.files[dir]; ok {
		return nil
	}
	m.filesLoading[dir] = true
	return func() tea.Msg { return filesMsg{dir: dir, list: listFiles(dir)} }
}

func (m *model) onFiles(msg filesMsg) {
	delete(m.filesLoading, msg.dir)
	m.files[msg.dir] = msg.list
	m.syncMenu()
}

// listFiles lists a folder's files relative to it, with forward slashes.
func listFiles(dir string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		var list []string
		seen := map[string]bool{}
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" && !seen[p] && len(list) < maxFiles {
				seen[p] = true
				list = append(list, p)
			}
		}
		return list
	}
	var list []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(list) >= maxFiles {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if p != dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			list = append(list, filepath.ToSlash(rel))
		}
		return nil
	})
	return list
}

// atToken finds an "@word" being typed at the cursor that is not at the very
// start of the message (that one is the agent menu): its rune range [from, to)
// and the text after "@".
func atToken(text []rune, cur int) (from, to int, query string, ok bool) {
	if cur > len(text) {
		return 0, 0, "", false
	}
	from = cur
	for from > 0 && !isSpaceRune(text[from-1]) {
		from--
	}
	if from == 0 || from >= len(text) || text[from] != '@' {
		return 0, 0, "", false
	}
	to = cur
	for to < len(text) && !isSpaceRune(text[to]) {
		to++
	}
	return from, to, string(text[from+1 : cur]), true
}

func isSpaceRune(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

// insertMention replaces runes [from, to) of the composer with "@path ".
func (m *model) insertMention(from, to int, path string) {
	t := m.comp.text
	if to < len(t) && isSpaceRune(t[to]) {
		to++ // reuse the space that follows
	}
	ins := []rune("@" + path + " ")
	nt := make([]rune, 0, len(t)-(to-from)+len(ins))
	nt = append(append(append(nt, t[:from]...), ins...), t[to:]...)
	m.comp.text = nt
	m.comp.cur = from + len(ins)
}
