package common

import (
	"testing"

	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestRenderMarkdownMemo asserts a memoized render matches a direct one
// and is dropped with the renderers when the styles change.
func TestRenderMarkdownMemo(t *testing.T) {
	sty := styles.DeckDark()
	const text = "Pick **one** of these:\n\n- `alpha`\n- beta"
	r := MarkdownRenderer(&sty, 1234)

	got, err := RenderMarkdownMemo(r, text)
	require.NoError(t, err)
	mu := LockMarkdownRenderer(r)
	mu.Lock()
	want, err := r.Render(text)
	mu.Unlock()
	require.NoError(t, err)
	require.Equal(t, want, got)

	again, err := RenderMarkdownMemo(r, text)
	require.NoError(t, err)
	require.Equal(t, got, again)

	mdMemoMu.Lock()
	_, cached := mdMemo[mdMemoKey{r: r, text: text}]
	mdMemoMu.Unlock()
	require.True(t, cached, "the render is memoized")

	gen := StyleGeneration()
	InvalidateStyleCaches()
	require.NotEqual(t, gen, StyleGeneration(), "a style change moves the generation")
	mdMemoMu.Lock()
	_, cached = mdMemo[mdMemoKey{r: r, text: text}]
	mdMemoMu.Unlock()
	require.False(t, cached, "invalidating the renderers drops their renders")
}

// TestRendererCacheIsBounded asserts the per-width renderer caches do not
// grow past their bound however many widths are asked for.
func TestRendererCacheIsBounded(t *testing.T) {
	sty := styles.DeckDark()
	for w := 2000; w < 2000+3*maxRenderersPerCache; w++ {
		_ = QuietMarkdownRenderer(&sty, w)
	}
	mdCacheMu.Lock()
	n := len(quietMDCache)
	mdCacheMu.Unlock()
	require.LessOrEqual(t, n, maxRenderersPerCache)
}
