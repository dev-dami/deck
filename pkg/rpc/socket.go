package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"

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
	V                 int    `json:"v"`
	Sock              string `json:"sock"`
	Token             string `json:"token"`
	PID               int    `json:"pid"`
	Version           string `json:"version"`
	BrowserCapability string `json:"browser_capability,omitempty"`
}

// NewToken returns a random per-launch token.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
			return dialSocket(ctx, sock)
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageBytes), grpc.MaxCallSendMsgSize(MaxMessageBytes)),
	)
	if err != nil {
		return nil, nil, err
	}
	return cc, deckv1.NewDeckClient(cc), nil
}
