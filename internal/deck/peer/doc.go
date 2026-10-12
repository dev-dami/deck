// Package peer links Deck instances (docs/peers-design.md, phase 1).
//
// Transport: Deck's own embedded SSH (golang.org/x/crypto/ssh), used only as
// an encrypted, mutually authenticated pipe. Each Deck has one ed25519 key in
// ~/.deck/peers/id_ed25519 (restricted to the current user) that is both its
// client identity and its host key. Peers are pinned by that key in
// ~/.deck/peers/pinned.json after pairing (pair.go); only pinned keys may
// connect and a dialed peer must present exactly its pinned key. There is no
// trust on first use outside pairing.
//
// The server accepts public-key auth only, the channel type "mcp" only and
// the global request keepalive@openssh.com only. Every other channel type
// (session, direct-tcpip, forwarded-tcpip, x11, ...), every channel request
// (shell, exec, pty-req, subsystem, env, ...) and every other global request
// (tcpip-forward, ...) is refused. Connections, handshakes, idle time, channel
// count, in-flight calls and message size are capped, and repeated failed
// handshakes lock an IP out with growing delays.
//
// Protocol on an mcp channel: newline-delimited JSON-RPC 2.0 (one message per
// line, at most DefaultMaxMessage bytes; a longer or malformed line gets an
// error reply and closes the channel), carrying MCP: initialize,
// notifications/initialized, ping, tools/list and tools/call. tools/list
// returns exactly two tools, search and execute, with the schemas of Deck's
// local MCP server; calls go to a Handler with the caller's pinned name.
//
// Both directions: whichever side dialed, each side serves MCP on the
// channels the other opens and can open its own (Conn.OpenMCP), so a Deck
// behind NAT only needs to dial out.
package peer
