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

func TestQRRows(t *testing.T) {
	t.Parallel()
	rows, err := QRRows("https://127.0.0.1:7878/#pair=ABCDE-23456")
	require.NoError(t, err)
	require.Greater(t, len(rows), 21)
	for _, r := range rows {
		require.Len(t, r, len(rows), "the code is square")
		require.Regexp(t, "^[01]+$", r)
	}
	require.Equal(t, strings.Repeat("0", len(rows)), rows[0], "the quiet zone is light")
	require.Contains(t, strings.Join(rows, ""), "1")
}
