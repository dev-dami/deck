package chat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// fullDiffContent is toolOutputDiffContent as it was before collapsed
// diffs rendered only their head: format the whole diff, then cut it.
func fullDiffContent(sty *styles.Styles, file, oldContent, newContent string, width int, expanded bool) string {
	bodyWidth := width - toolBodyLeftPaddingTotal
	formatter := common.DiffFormatter(sty).Before(file, oldContent).After(file, newContent).Width(bodyWidth)
	if width > maxTextWidth {
		formatter = formatter.Split()
	}
	formatted := formatter.String()
	lines := strings.Split(formatted, "\n")
	maxLines := collapsedMaxLines(len(lines))
	if expanded {
		maxLines = len(lines)
	}
	if len(lines) > maxLines && !expanded {
		truncMsg := sty.Tool.DiffTruncation.Width(bodyWidth).
			Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-maxLines))
		formatted = strings.Join(lines[:maxLines], "\n") + "\n" + truncMsg
	}
	return sty.Tool.Body.Render(formatted)
}

// TestDiffHead_MatchesFullRender asserts a diff rendered only up to its
// collapsed head is byte-identical to cutting the full render, in both
// layouts, collapsed and expanded, with and without truncation.
func TestDiffHead_MatchesFullRender(t *testing.T) {
	t.Parallel()

	sty := styles.DeckDark()
	var before, after strings.Builder
	for i := range 60 {
		fmt.Fprintf(&before, "func f%d() int {\n\treturn %d\n}\n\n", i, i)
		if i%3 == 0 {
			fmt.Fprintf(&after, "func f%d() int {\n\treturn %d // changed\n}\n\n", i, i*2)
		} else {
			fmt.Fprintf(&after, "func f%d() int {\n\treturn %d\n}\n\n", i, i)
		}
	}
	cases := map[string][2]string{
		"long":  {before.String(), after.String()},
		"short": {"a\nb\nc\n", "a\nB\nc\n"},
		"same":  {"a\nb\n", "a\nb\n"},
		"new":   {"", "x\ny\nz\n"},
	}
	for name, c := range cases {
		for _, width := range []int{70, 200} {
			for _, expanded := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/w%d/expanded=%v", name, width, expanded), func(t *testing.T) {
					t.Parallel()
					want := fullDiffContent(&sty, "main.go", c[0], c[1], width, expanded)
					got := toolOutputDiffContent(&sty, "main.go", c[0], c[1], width, expanded)
					require.Equal(t, want, got)
				})
			}
		}
	}
}
