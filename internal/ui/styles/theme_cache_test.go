package styles

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"github.com/stretchr/testify/require"
)

// saveOldTheme writes theme file name in dir and dates it at, so the
// cache may keep what is built from it.
func saveOldTheme(t *testing.T, dir, name string, tf *ThemeFile, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	require.NoError(t, SaveThemeFile(path, tf))
	require.NoError(t, os.Chtimes(path, at, at))
}

// requireFreshTheme loads name and checks it against a build that skips
// the cache; it reports whether the load came from the cache.
func requireFreshTheme(t *testing.T, name, step string) (Styles, bool) {
	t.Helper()
	key := normalizeThemeName(name)
	stamp, _, _ := themeDirStamp(time.Now())
	_, hit := cachedTheme(key, stamp)
	got, err := LoadTheme(name)
	require.NoError(t, err, step)
	want, err := buildTheme(name, key)
	require.NoError(t, err, step)
	require.Equal(t, want, got, step)
	return got, hit
}

func TestLoadThemeCacheInvalidatesOnChange(t *testing.T) {
	dir := t.TempDir()
	setTestThemeDirs(t, []string{dir})
	old := time.Now().Add(-time.Hour)

	saveOldTheme(t, dir, "mine", &ThemeFile{Base: "nord", Palette: Palette{Primary: "#ff0000"}}, old)
	saveOldTheme(t, dir, "child", &ThemeFile{Base: "mine", Palette: Palette{Secondary: "#00ff00"}}, old)
	_, hit := requireFreshTheme(t, "mine", "first load")
	require.False(t, hit)
	first, hit := requireFreshTheme(t, "mine", "second load")
	require.True(t, hit, "an unchanged directory serves the kept build")
	_, _ = requireFreshTheme(t, "child", "child")

	// Same size, a later time: the edit is seen.
	saveOldTheme(t, dir, "mine", &ThemeFile{Base: "nord", Palette: Palette{Primary: "#0000ff"}}, old.Add(time.Minute))
	edited, hit := requireFreshTheme(t, "mine", "edited")
	require.False(t, hit)
	require.NotEqual(t, first.Markdown, edited.Markdown, "the edit changes the theme")
	_, hit = requireFreshTheme(t, "child", "child after its base was edited")
	require.False(t, hit, "editing a base invalidates the themes built on it")

	// Same time, another size: the edit is seen.
	saveOldTheme(t, dir, "mine", &ThemeFile{Base: "nord", Palette: Palette{Primary: "#00ffff", Accent: "#123456"}}, old.Add(time.Minute))
	_, hit = requireFreshTheme(t, "mine", "resized")
	require.False(t, hit)

	// A file appearing shadows a built-in; removing it brings it back.
	_, _ = requireFreshTheme(t, "dracula", "built-in")
	_, hit = requireFreshTheme(t, "dracula", "built-in again")
	require.True(t, hit)
	saveOldTheme(t, dir, "dracula", &ThemeFile{Base: "dracula", Palette: Palette{Primary: "#ff00ff"}}, old)
	_, hit = requireFreshTheme(t, "dracula", "shadowed")
	require.False(t, hit)
	require.NoError(t, os.Remove(filepath.Join(dir, "dracula.json")))
	_, hit = requireFreshTheme(t, "dracula", "unshadowed")
	require.False(t, hit)
}

// TestLoadThemeCacheRacyWrite rewrites a theme within one modification
// time tick, keeping its size: what was built from the first write must
// not be served for the second.
func TestLoadThemeCacheRacyWrite(t *testing.T) {
	dir := t.TempDir()
	setTestThemeDirs(t, []string{dir})
	now := time.Now()
	saveOldTheme(t, dir, "mine", &ThemeFile{Base: "nord", Palette: Palette{Primary: "#ff0000"}}, now)
	before, _ := requireFreshTheme(t, "mine", "just written")
	saveOldTheme(t, dir, "mine", &ThemeFile{Base: "nord", Palette: Palette{Primary: "#0000ff"}}, now)
	after, hit := requireFreshTheme(t, "mine", "rewritten in the same tick")
	require.False(t, hit, "a file this fresh is never kept")
	require.NotEqual(t, before.Markdown, after.Markdown)
}

// TestLoadThemeCacheNotWrittenThrough renders markdown with a kept theme's
// configs, which it shares with every later load, and checks they still
// match a fresh build: nothing writes through their pointers.
func TestLoadThemeCacheNotWrittenThrough(t *testing.T) {
	setTestThemeDirs(t, []string{t.TempDir()})
	for _, name := range BuiltinThemeNames() {
		s, err := LoadTheme(name)
		require.NoError(t, err)
		for _, cfg := range []ansi.StyleConfig{s.Markdown, s.QuietMarkdown, s.PlanMarkdown} {
			r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(60), glamour.WithChromaFormatter("terminal16m"))
			require.NoError(t, err)
			_, err = r.Render(markdownSample)
			require.NoError(t, err)
		}
		kept, hit := cachedTheme(name, mustStamp(t))
		require.True(t, hit, name)
		fresh, err := buildTheme(name, name)
		require.NoError(t, err)
		require.Equal(t, fresh.Markdown, kept.Markdown, name)
		require.Equal(t, fresh.QuietMarkdown, kept.QuietMarkdown, name)
		require.Equal(t, fresh.PlanMarkdown, kept.PlanMarkdown, name)
	}
}

func mustStamp(t *testing.T) string {
	t.Helper()
	stamp, _, ok := themeDirStamp(time.Now())
	require.True(t, ok)
	return stamp
}

const markdownSample = "# Title\n\n## Section\n\nSome *emphasis*, **strong**, `code` and a [link](https://example.com).\n\n" +
	"- one\n- two\n  1. nested\n\n> a quote\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
	"```go\nfunc main() { fmt.Println(\"hi\") }\n```\n\n---\n\n- [x] done\n- [ ] todo\n"

// benchThemeDir is a theme directory holding a few user themes, one of
// them built on another, as the theme picker's cursor would walk it.
func benchThemeDir(b *testing.B) {
	dir := b.TempDir()
	orig := themeDirsOverride
	themeDirsOverride = []string{dir}
	b.Cleanup(func() { themeDirsOverride = orig })
	files := map[string]*ThemeFile{
		"mine":  {Base: "nord", Palette: Palette{Primary: "#ff0000"}},
		"child": {Base: "mine", Palette: Palette{Secondary: "#00ff00"}},
		"other": {Base: "dracula"},
	}
	old := time.Now().Add(-time.Hour)
	for name, tf := range files {
		path := filepath.Join(dir, name+".json")
		require.NoError(b, SaveThemeFile(path, tf))
		require.NoError(b, os.Chtimes(path, old, old))
	}
}

// BenchmarkLoadTheme loads a built-in, a user theme and a user theme
// built on another: by value (LoadTheme), and the way the theme picker
// previews each cursor move (LoadThemeShared, copied into the active
// styles), with the directory unchanged (kept) and with a cold cache
// (built).
func BenchmarkLoadTheme(b *testing.B) {
	benchThemeDir(b)
	active := new(Styles)
	for _, name := range []string{"dracula", "mine", "child"} {
		b.Run("value/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := LoadTheme(name); err != nil {
					b.Fatal(err)
				}
			}
		})
		for _, cold := range []bool{false, true} {
			sub := "kept"
			if cold {
				sub = "built"
			}
			b.Run("preview/"+sub+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if cold {
						themeCache.Lock()
						themeCache.entries = nil
						themeCache.Unlock()
					}
					s, err := LoadThemeShared(name)
					if err != nil {
						b.Fatal(err)
					}
					*active = *s
				}
			})
		}
	}
}

func BenchmarkStylesCopy(b *testing.B) {
	s := DeckDark()
	b.Run("Clone", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = s.Clone()
		}
	})
	b.Run("assign", func(b *testing.B) {
		d := new(Styles)
		b.ReportAllocs()
		for b.Loop() {
			*d = s
		}
	})
}
