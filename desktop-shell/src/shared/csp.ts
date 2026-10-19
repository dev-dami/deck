// The renderer's Content-Security-Policy. The built page carries it as a meta
// tag and the app:// protocol sends it as a header. The renderer opens no
// connection of its own (connect-src 'none'): main owns the socket.
export const RENDERER_CSP = [
  "default-src 'none'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self' data: blob:",
  "font-src 'self'",
  "connect-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "object-src 'none'",
].join("; ");
