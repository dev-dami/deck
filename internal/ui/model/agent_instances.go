package model

import (
	"github.com/dev-dami/deck/internal/ui/completions"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

// instanceItems are the $ popup rows about who, not which model: for a kind
// with several live members in the session, one row per member ($claude2) and
// one for all of them ($claude*); for every kind with a live member, one that
// starts another ($claude+). Lines are the session's roster; an empty
// session (whose roster lists the installed CLIs as "ready") has none.
func instanceItems(lines []workspace.AgentLine) []completions.AgentItem {
	var kinds []string
	byKind := map[string][]workspace.AgentLine{}
	for _, l := range lines {
		if l.Status == "exited" || l.Status == "ready" {
			continue
		}
		if _, ok := byKind[l.Kind]; !ok {
			kinds = append(kinds, l.Kind)
		}
		byKind[l.Kind] = append(byKind[l.Kind], l)
	}
	var out []completions.AgentItem
	for _, k := range kinds {
		mark := tone.Mark(k) + " "
		if ms := byKind[k]; len(ms) > 1 {
			for _, l := range ms {
				out = append(out, completions.AgentItem{
					Label: mark + l.ID + " · " + l.Status,
					Value: completions.AgentCompletionValue{Kind: k, Insert: "$" + l.ID},
				})
			}
			out = append(out, completions.AgentItem{
				Label: mark + k + "* · every " + k,
				Value: completions.AgentCompletionValue{Kind: k, Insert: "$" + k + "*"},
			})
		}
		out = append(out, completions.AgentItem{
			Label: mark + k + "+ · start another " + k,
			Value: completions.AgentCompletionValue{Kind: k, Insert: "$" + k + "+"},
		})
	}
	return out
}
