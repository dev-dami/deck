package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootCommands(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"doctor", "dirs", "projects", "logs", "session"} {
		require.True(t, names[want], "missing %q command", want)
	}
	for _, gone := range []string{"login", "logout", "run", "models", "stats", "schema", "update-providers", "server"} {
		require.False(t, names[gone], "removed command %q is still registered", gone)
	}
}

func TestRootFlags(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"yolo", "session", "continue"} {
		require.NotNil(t, rootCmd.Flags().Lookup(name), "missing --%s", name)
	}
	for _, name := range []string{"cwd", "data-dir", "debug"} {
		require.NotNil(t, rootCmd.PersistentFlags().Lookup(name), "missing --%s", name)
	}
}
