//go:build !windows

package rpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// maxSockPath keeps the path inside sockaddr_un on every OS (104 on macOS).
const maxSockPath = 100

// Listen opens a Unix socket for this process under dataDir/run, or in the
// temp dir when that path would be too long. The directory is private to
// the user and the socket is removed by the returned cleanup.
func Listen(dataDir string) (net.Listener, string, func(), error) {
	name := fmt.Sprintf("deck-%d.sock", os.Getpid())
	dir := filepath.Join(dataDir, "run")
	if len(filepath.Join(dir, name)) > maxSockPath {
		dir = filepath.Join(os.TempDir(), "deck-run")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", nil, err
	}
	sock := filepath.Join(dir, name)
	_ = os.Remove(sock) // a stale one from a crashed run with our pid
	l, err := net.Listen("unix", sock)
	if err != nil {
		return nil, "", nil, fmt.Errorf("listen on %s: %w", sock, err)
	}
	_ = os.Chmod(sock, 0o600)
	return l, sock, func() { _ = l.Close(); _ = os.Remove(sock) }, nil
}

func dialSocket(ctx context.Context, sock string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", sock)
}
