// The deck.v1 schema the tests serve and call. The profile RPCs and the
// profile and role of AgentLine and AgentNode
// (docs/design/agents-and-mentions.md, section 4) come from the engine's
// change to proto/deck/v1/deck.proto (claude/named-agents-engine); until
// that lands here, the tests add them to a copy, with that change's field
// numbers, so the fakes and the client agree. Once deck.proto has them,
// this is the repository's file unchanged.
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const REPO_PROTO = join(__dirname, "..", "..", "proto", "deck", "v1", "deck.proto");

const RPCS = `
  rpc ListProfiles(ListProfilesRequest) returns (ListProfilesResponse);
  rpc SaveProfile(SaveProfileRequest) returns (SaveProfileResponse);
  rpc DeleteProfile(DeleteProfileRequest) returns (DeleteProfileResponse);
`;

const MESSAGES = `
message Profile {
  string name = 1;
  string kind = 2;
  string model = 3;
  string effort = 4;
  string role = 5;
  string instructions = 6;
  string voice = 7;
  string color = 8;
  repeated string paths = 9;
  string scope = 10;
}
message ListProfilesRequest {}
message ListProfilesResponse {
  repeated Profile profiles = 1;
}
message SaveProfileRequest {
  Profile profile = 1;
}
message SaveProfileResponse {
  Profile profile = 1;
}
message DeleteProfileRequest {
  string name = 1;
  string scope = 2;
}
message DeleteProfileResponse {}
`;

function withProfiles(src: string): string {
  let out = src;
  if (!/\brpc ListProfiles\(/.test(out)) {
    const events = out.indexOf("  rpc Events(");
    if (events < 0) throw new Error("deck.proto: no rpc Events to add the profile RPCs before");
    out = `${out.slice(0, events)}${RPCS.trimStart()}\n${out.slice(events)}${MESSAGES}`;
  }
  const fields: Array<[string, number]> = [
    ["AgentLine", 7],
    ["AgentNode", 15],
  ];
  for (const [msg, n] of fields) {
    if (new RegExp(`message ${msg} \\{[^}]*\\brole\\b`).test(out)) continue;
    const before = out;
    out = out.replace(new RegExp(`(message ${msg} \\{[^}]*?)(\\n\\})`), `$1\n  string profile = ${n};\n  string role = ${n + 1};$2`);
    if (out === before) throw new Error(`deck.proto: no message ${msg} to add profile and role to`);
  }
  return out;
}

let cached: string | null = null;

export function testProto(): string {
  if (cached !== null) return cached;
  const src = readFileSync(REPO_PROTO, "utf8");
  const out = withProfiles(src);
  if (out === src) cached = REPO_PROTO;
  else {
    cached = join(mkdtempSync(join(tmpdir(), "deck-proto-")), "deck.proto");
    writeFileSync(cached, out);
  }
  return cached;
}
