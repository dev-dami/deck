// Package wire is the JSON protocol between the Deck remote server and the
// phone client: the frame envelope, the entities, the method and event
// payloads and the pairing HTTP bodies of docs/remote-contract.md §3,
// protocol version 2.
//
// It holds types and constants only; the few Validate methods and Truncate
// are the whole of its logic. The fixtures in testdata/ are the only surface
// shared with pkg/remote/web: both sides must decode every fixture and encode
// it back byte-equal after key sorting, and Fixtures lists them.
package wire
