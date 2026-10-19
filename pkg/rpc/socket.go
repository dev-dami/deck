package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// Protocol is the handshake version; it changes when deck.v1 breaks.
const Protocol = 1

// TokenHeader is the metadata key that carries "Bearer <token>".
const TokenHeader = "authorization"

// Handshake is the one JSON line `deck serve --rpc` prints on stdout once
// it is listening: where to dial and the token to present.
type Handshake struct {
	V       int    `json:"v"`
	Sock    string `json:"sock"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// maxSockPath keeps the path inside sockaddr_un on every OS (104 on macOS).
const maxSockPath = 100

// NewToken returns a random per-launch token.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Listen opens a Unix socket for this process under dataDir/run, or in the
// temp dir when that path would be too long. AF_UNIX works on Windows 10
// 1803 and later as well. The directory is private to the user and the
// socket is removed by the returned cleanup.
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

type bearer string

func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{TokenHeader: "Bearer " + string(b)}, nil
}

// The socket is private to the user; the token, not TLS, is the guard.
func (bearer) RequireTransportSecurity() bool { return false }

// Dial connects to a `deck serve --rpc` socket.
func Dial(sock, token string) (*grpc.ClientConn, deckv1.DeckClient, error) {
	cc, err := grpc.NewClient("passthrough:///deck",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bearer(token)),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageBytes), grpc.MaxCallSendMsgSize(MaxMessageBytes)),
	)
	if err != nil {
		return nil, nil, err
	}
	return cc, deckv1.NewDeckClient(cc), nil
}
