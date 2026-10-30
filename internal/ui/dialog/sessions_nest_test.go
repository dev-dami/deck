package dialog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/workspace"
)

type parentRooms map[string]string

func (p parentRooms) Room(id string) workspace.Room { return workspace.Room{Parent: p[id]} }

// Sub-threads sit under their parent, at their depth; one whose parent is
// not listed stands on its own; a workspace with no rooms keeps the order.
func TestNestSessionsPutsSubThreadsUnderTheirParent(t *testing.T) {
	t.Parallel()
	list := []session.Session{{ID: "a"}, {ID: "b"}, {ID: "a1"}, {ID: "a1x"}, {ID: "orphan"}}
	rooms := parentRooms{"a1": "a", "a1x": "a1", "orphan": "gone"}
	out, depths := nestSessions(rooms, list)
	var ids []string
	for _, s := range out {
		ids = append(ids, s.ID)
	}
	require.Equal(t, []string{"a", "a1", "a1x", "b", "orphan"}, ids)
	require.Equal(t, map[string]int{"a": 0, "a1": 1, "a1x": 2, "b": 0, "orphan": 0}, depths)

	same, _ := nestSessions(struct{}{}, list)
	require.Equal(t, list, same)
}
