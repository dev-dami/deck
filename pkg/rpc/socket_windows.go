package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Listen opens a named pipe for this process. Windows has AF_UNIX too, but
// Node (and so the desktop app) dials a path on Windows as a named pipe and
// cannot reach an AF_UNIX socket. The name carries a random part so no one
// can claim it first, and the pipe admits only the current user. dataDir is
// unused: a pipe lives in the pipe namespace, not on disk.
func Listen(dataDir string) (net.Listener, string, func(), error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, "", nil, err
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	sock := fmt.Sprintf(`\\.\pipe\deck-%d-%s`, os.Getpid(), hex.EncodeToString(b))
	l, err := winio.ListenPipe(sock, &winio.PipeConfig{
		SecurityDescriptor: fmt.Sprintf("D:P(A;;GA;;;%s)", sid),
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("listen on %s: %w", sock, err)
	}
	return l, sock, func() { _ = l.Close() }, nil
}

func dialSocket(ctx context.Context, sock string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, sock)
}

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read the current user: %w", err)
	}
	return u.User.Sid.String(), nil
}
