package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoomReadsLiveAndStoredThreads(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	require.Equal(t, Room{}, w.Room("s1"))

	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads["s1"], w.back[tid] = tid, "s1"
	_, err = w.mgr().AddMember(tid, "claude", "") // the thread now has content to store
	require.NoError(t, err)
	live := w.Room("s1")
	require.NotEmpty(t, live.Dir)
	require.False(t, live.Archived)

	// Archived, the thread is no longer live: Room reads the store.
	require.NoError(t, w.mgr().Archive(tid))
	w.mgr().Flush()
	require.Equal(t, Room{Dir: live.Dir, Archived: true}, w.Room("s1"))
}
