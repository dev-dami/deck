# Named agents, `@` mentions and `#` context

Status: accepted, 2026-10-26. Deck becomes an agent development environment.
Agents can be named ("Charles"), carry a role and an optional voice, and are
addressed with `@`, as people are. Files and other context move to `#`.

## Goals

- **Named agents.** A crew of named agents with roles reads better than
  `claude-1` and `codex-2`, and it scales past one agent of each kind.
- **One mention syntax for agents and people.** `@charles`, `@claude` and
  `@alice` (a teammate) all mean "I'm talking to you". This is the
  convention from Slack and GitHub.
- **Files get their own sigil.** Context (files, and later symbols, diffs,
  tasks and threads) moves to `#`, which has precedent in VS Code Copilot's
  `#file`.
- **Nothing breaks.** `$` keeps working everywhere it works today.

## Non-goals (later)

Memory across threads, avatars, sharing profiles between people, and
personalities beyond a one-line voice.

## 1. Profiles

A profile is a named recipe for an agent. It holds:

| Field | Type | Meaning |
|---|---|---|
| `name` | string, required | How it is addressed and shown (`charles`). The same rules as `Manager.Rename`: non-empty; no whitespace and none of `@ $ / * + #`; not `all`, `user`, `deck`, an agent kind, or a teammate's name. Case-insensitive and unique. |
| `kind` | string, required | The CLI it runs: `claude`, `codex`, `opencode` or `antigravity`. |
| `model`, `effort` | string, optional | Defaults to the kind's defaults. |
| `role` | string, optional | One line ("reviewer", "implements the plan"). Shown in the roster, and goes into the agent's briefing. |
| `instructions` | string, optional | Standing instructions, appended to the agent's briefing. Counted against the context policy like any briefing text. |
| `voice` | string, optional | One line of personality ("dry, terse, British"). Appended to the briefing after `instructions`. Optional by design: the value of a profile is its role, not its voice. |
| `paths` | string list, optional | Globs the agent should keep to. They go into the briefing, and Deck's file claims hold it to them where the engine supports that. |
| `color` | string, optional | A CSS colour for its hue. Defaults to the kind's hue. |

**Where profiles live:** the `profiles` list in `deck.json`.
- Global: `~/.config/deck/deck.json`.
- Project: the project's `deck.json` / `.deck/deck.json`, following the existing config lookup.

A project profile with the same name overrides the global one. Unknown
fields are ignored. Invalid profiles are skipped, with a warning in
`deck doctor`.

**The name never hides the kind.** Every client shows a named agent with
its kind's glyph and hue (`✳ charles`, or `>_ ada`). Cost, plan limits and
failures belong to the kind.

## 2. Addressing

The engine (`internal/deck/session/address.go`) accepts **both `@` and `$`**
as the agent sigil, in user messages (`ParseAddressing`), in agents'
replies (`parseMentions`) and in races (`scanRace`). `TrimSigil` already
strips both. A name resolves in this order:

1. `all`;
2. a kind (`@claude`), which reaches the pool of that kind as today;
3. a member of the thread, by name, then by id;
4. **a profile** (`@charles` with no member of that name in the thread):
   - Deck adds a member of the profile's kind, named after the profile, with
     its model and effort;
   - the profile's role, instructions, voice and paths go into the member's
     briefing;
   - the member keeps its profile name for the thread's life (a later
     `@charles` reaches the same member).
5. Otherwise the token is plain text, as `$HOME` is today.

The kind suffixes and races carry over unchanged: `@claude+` adds a
Claude, `@claude*` reaches every Claude, `@claude×3` races three.
`@charles+` adds a second member from the profile, named `charles-2`.

`@` matches only names, so `@src/foo.ts` and `user@example.com` stay text:
`scanMention` already refuses a name followed by `/` or by `.` and a
non-space. The clause-start rule (a mention counts at the start of the
text, a line or a clause) stays.

**Briefings** teach agents `@name` for hand-offs, list the thread's
members with their names, roles and kinds, and say that `$name` also
works.

**Teammates:** `@alice` reaches a teammate wherever Deck's team features
already route a message to a person. Otherwise it is plain text. Profile
names may not shadow teammates.

## 3. `#` context

`#` opens the context picker in every client. It offers files first (the
room's changed files, then the tree, as `@` does today). Later it will
offer symbols, diffs, tasks and threads. A chosen file is inserted
**exactly as an `@path` file reference is inserted today**, so the engine
and the agents see the same text as before. That keeps the change
client-side: the CLIs already understand `@path`.

**Transition:** typing `@` followed by something path-like (it contains
`/`, or ends in `.ext`) still offers files, so `@`-for-files muscle memory
keeps working.

## 4. API

Add these to `proto/deck/v1/deck.proto` and regenerate (`buf.gen.yaml`),
following the patterns already there:

- `message Profile { string name, kind, model, effort, role, instructions, voice, color = …; repeated string paths = …; string scope = …; }`.
  `scope` is `"global"` or `"project"`.
- `rpc ListProfiles(ListProfilesRequest) returns (ListProfilesResponse)`:
  the merged list, project over global, each with its scope.
- `rpc SaveProfile(SaveProfileRequest{Profile profile}) returns (SaveProfileResponse)`:
  validates (the rules in section 1) and writes to the given scope's
  `deck.json`.
- `rpc DeleteProfile(DeleteProfileRequest{string name; string scope}) returns (DeleteProfileResponse)`.
- The roster line or member message gains `string profile` (the profile
  a member came from) and `string role`, so clients can show them.

The phone's `pkg/remote` wire and adapter carry the same `profile`/`role`
fields, if they mirror the roster.

## 5. Clients

- **TUI** (`internal/ui/model`): `@` opens the agents picker (with
  profiles and teammates), `#` opens files, and `$` stays as an alias for
  agents. The roster shows `glyph name · role`. Settings or a `/profiles`
  command lists profiles; editing them in the TUI is optional for the
  first version.
- **Desktop** (`desktop-shell/src/renderer`):
  - `completion.ts` and the composer's pickers: `@` → "Agents and people",
    `#` → "Files", `$` → agents.
  - A profiles editor component (`ui/ProfilesEditor.tsx`) in Settings >
    Agents, which edits global and project profiles through the API.
  - Named members show their glyph, name and role in the lanes, timeline
    headers and the graph (names already flow through member names).
- **Phone** (`pkg/remote/web`): the same pickers. Profile editing is not
  needed there.
- **Docs:** the README's addressing sections and the in-app help lead with
  `@`, mention `$`, and introduce `#`.

## 6. Tests

- **Engine:**
  - `@` and `$` parse identically, across every existing addressing case
    (table tests in `address_test.go`).
  - A profile mention spawns a correctly configured member, and a second
    mention reaches it.
  - Profile validation, including the reserved and colliding names.
  - `parseMentions` accepts `@name` lines.
  - `@path` and emails stay text.
- **Config:** loading, merging (project over global), and invalid-entry
  handling.
- **Clients:** the picker triggers, path-like `@` still offering files,
  and the profiles editor's validation.
