#!/usr/bin/env node
// A stand-in for the deck binary in tests: `deck serve --rpc` prints the
// handshake in DECK_FAKE_HANDSHAKE (of a fake gRPC server the test runs)
// and lives until its stdin closes, as Deck does. It writes "stopped" to
// DECK_FAKE_STOPPED when it ends that way.
const fs = require("node:fs");

const args = process.argv.slice(2);
if (args[0] !== "serve" || !args.includes("--rpc")) {
  process.stderr.write("fake deck: only 'serve --rpc' is known\n");
  process.exit(2);
}
const handshake = process.env.DECK_FAKE_HANDSHAKE;
if (handshake === undefined) {
  process.stderr.write("fake deck: DECK_FAKE_HANDSHAKE is not set\n");
  process.exit(1);
}
// Deck may log before it listens; the app skips lines that are no handshake.
process.stdout.write("Deck is starting\n");
process.stdout.write(`${handshake}\n`);
// DECK_FAKE_EXIT_AFTER (ms) makes it stop by itself, as a crash would.
if (process.env.DECK_FAKE_EXIT_AFTER) {
  setTimeout(() => {
    process.stderr.write("fake deck: the store is locked\npanic: fake crash\n", () => process.exit(3));
  }, Number(process.env.DECK_FAKE_EXIT_AFTER));
}
process.stdin.resume();
process.stdin.on("end", () => {
  if (process.env.DECK_FAKE_STOPPED) fs.writeFileSync(process.env.DECK_FAKE_STOPPED, "stopped");
  process.exit(0);
});
