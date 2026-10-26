package styles

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The theme picker loads a theme on every cursor move, and building one is
// about a millisecond of file reads, palette resolution and style
// construction. LoadTheme keeps what it built, keyed by the name and a
// stamp of the theme directory, so moving back over a theme only lists
// the directory.
//
// The stamp is every theme file's name, size and modification time: a
// user theme can be built on another one, and a file appearing can shadow
// a built-in, so any change in the directory invalidates every entry.
//
// A kept Styles is never modified: LoadTheme hands out a copy of it, and
// LoadThemeShared the kept one itself for callers that copy it where it
// goes. A copy is shallow, so what stays shared are the values the glamour
// style configs (Markdown, QuietMarkdown, PlanMarkdown) point at. Nothing
// writes through those pointers, here or in glamour
// (TestLoadThemeCacheNotWrittenThrough renders with a kept theme and
// checks), so they are not deep-copied: [Styles.Clone] costs about half
// of the build it would save.

const (
	// themeCacheMax bounds the cache; it is emptied when full. The
	// built-ins and a handful of user themes fit.
	themeCacheMax = 32
	// themeCacheRacy: a build is not kept while a theme file changed less
	// than this long ago. A file's modification time only moves in clock
	// ticks, so a second write that soon could leave the stamp as it was.
	themeCacheRacy = 2 * time.Second
)

var themeCache struct {
	sync.Mutex
	entries map[string]themeCacheEntry
}

type themeCacheEntry struct {
	stamp  string
	styles *Styles
}

// themeDirStamp is the theme directory's stamp, and whether a build made
// now may be kept under it (no theme file changed too recently). ok is
// false when the directory cannot be read.
func themeDirStamp(now time.Time) (stamp string, keep, ok bool) {
	dir := ThemeDirs()[0]
	var b strings.Builder
	b.WriteString(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return b.String(), true, true
		}
		return "", false, false
	}
	keep = true
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return "", false, false
		}
		if mt := info.ModTime(); !mt.Before(now.Add(-themeCacheRacy)) {
			keep = false
		}
		b.WriteByte(0)
		b.WriteString(e.Name())
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
	}
	return b.String(), keep, true
}

// cachedTheme is the theme key built under stamp, if kept.
func cachedTheme(key, stamp string) (*Styles, bool) {
	themeCache.Lock()
	defer themeCache.Unlock()
	e, ok := themeCache.entries[key]
	if !ok || e.stamp != stamp {
		return nil, false
	}
	return e.styles, true
}

// keepTheme keeps s as theme key built under stamp.
func keepTheme(key, stamp string, s *Styles) {
	themeCache.Lock()
	defer themeCache.Unlock()
	if themeCache.entries == nil || len(themeCache.entries) >= themeCacheMax {
		themeCache.entries = make(map[string]themeCacheEntry)
	}
	themeCache.entries[key] = themeCacheEntry{stamp: stamp, styles: s}
}
