package remote

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderQR(t *testing.T) {
	t.Parallel()
	out, err := RenderQR("https://127.0.0.1:7878/#pair=ABCDE-23456")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Greater(t, len(lines), 10)
	for _, l := range lines {
		require.True(t, strings.HasPrefix(l, "\x1b[30;47m"), "every line is black on white")
		require.True(t, strings.HasSuffix(l, "\x1b[0m"))
	}
	require.Contains(t, out, "█")
}
