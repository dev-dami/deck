package remote

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderQRShape(t *testing.T) {
	out, err := RenderQR("https://example.trycloudflare.com/#pair=abc")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.Greater(t, len(lines), 10)

	width := -1
	for _, l := range lines {
		require.True(t, strings.HasPrefix(l, "\x1b[30;47m"), "black on white on every line")
		require.True(t, strings.HasSuffix(l, "\x1b[0m"), "reset at the end of every line")
		w := len([]rune(strings.TrimSuffix(strings.TrimPrefix(l, "\x1b[30;47m"), "\x1b[0m")))
		if width < 0 {
			width = w
		}
		require.Equal(t, width, w, "all lines are as wide as the code")
	}
	require.Contains(t, out, "█")
}

func TestRenderQRQuietZoneIsBlank(t *testing.T) {
	out, err := RenderQR("x")
	require.NoError(t, err)
	first := strings.SplitN(out, "\n", 2)[0]
	body := strings.TrimSuffix(strings.TrimPrefix(first, "\x1b[30;47m"), "\x1b[0m")
	require.Equal(t, strings.Repeat(" ", len([]rune(body))), body, "the top margin has no modules")
}

func TestRenderQRDeterministic(t *testing.T) {
	a, _ := RenderQR("same")
	b, _ := RenderQR("same")
	require.Equal(t, a, b)
}

func TestRenderQRTooLong(t *testing.T) {
	_, err := RenderQR(strings.Repeat("x", 10000))
	require.Error(t, err)
}
