import type { Person, ProfileRef } from "@web/mentions";
import { useEffect, useMemo, useSyncExternalStore } from "react";
import { type DeckClient, serves } from "./client";
import type { Team } from "./crew";

// Profiles (docs/design/agents-and-mentions.md, section 1): a named recipe
// for an agent. "@charles" starts a member of the profile's kind named
// charles, with its model, effort, role, instructions, voice and paths.

export type ProfileScope = "global" | "project";

export interface Profile {
  name: string;
  kind: string;
  model: string;
  effort: string;
  role: string;
  instructions: string;
  voice: string;
  paths: string[];
  color: string;
  scope: ProfileScope;
}

export const emptyProfile = (kind: string, scope: ProfileScope): Profile => ({
  name: "",
  kind,
  model: "",
  effort: "",
  role: "",
  instructions: "",
  voice: "",
  paths: [],
  color: "",
  scope,
});

// The names an agent may not take: Deck's own words. Kinds and teammates
// are refused too.
export const RESERVED_NAMES = ["all", "user", "deck"];

// What a name may not hold, as Manager.Rename refuses it.
const BAD_NAME = /[\s@$/*+#]/;
// What "@" reads as a name (address.go, isNameByte), so what a profile's
// name is made of; and its longest (profile.MaxNameRunes).
const NAME = /^[\w-]+$/;
export const MAX_NAME = 32;

export type ProfileErrors = Partial<Record<"name" | "kind" | "role" | "voice" | "paths" | "color", string>>;

export interface ProfileRules {
  // The agent kinds this Deck runs.
  kinds: readonly string[];
  // The team's people, whose names a profile may not shadow.
  teammates?: readonly string[];
  // The other profiles: a name is unique within its scope.
  profiles: readonly Profile[];
  // The name of the profile being edited, which it may keep.
  editing?: { name: string; scope: ProfileScope } | null;
  // Whether a CSS colour parses; the browser's own check by default.
  isColour?: (v: string) => boolean;
}

// The characters Deck takes a colour in (profile.plainColor): nothing that
// could end a CSS declaration.
const PLAIN_COLOUR = /^[\w\-#()%., /]{1,64}$/;

const cssColour = (v: string): boolean => typeof CSS === "undefined" || CSS.supports("color", v);
const same = (a: string, b: string): boolean => a.toLowerCase() === b.toLowerCase();

// The problems with p, by field; empty when it can be saved. The engine
// checks the same rules (SaveProfile) and has the last word.
export function validateProfile(p: Profile, rules: ProfileRules): ProfileErrors {
  const errors: ProfileErrors = {};
  const name = p.name.trim();
  if (name === "") errors.name = "A profile needs a name.";
  else if (BAD_NAME.test(name)) errors.name = "No spaces, and none of @ $ / * + #.";
  else if (!NAME.test(name)) errors.name = `Only letters, digits, - and _, so that @${name} reaches it.`;
  else if (name.length > MAX_NAME) errors.name = `At most ${MAX_NAME} characters.`;
  else if (RESERVED_NAMES.some((r) => same(r, name))) errors.name = `“${name}” is reserved by Deck.`;
  else if (rules.kinds.some((k) => same(k, name))) errors.name = `“${name}” is an agent kind. Pick a name of its own.`;
  else if ((rules.teammates ?? []).some((t) => same(t, name))) errors.name = `${name} is a teammate. Pick another name.`;
  else {
    const ed = rules.editing ?? null;
    const taken = rules.profiles.some(
      (o) => o.scope === p.scope && same(o.name, name) && !(ed !== null && ed.scope === o.scope && same(ed.name, o.name)),
    );
    if (taken) errors.name = `There is already a ${p.scope} profile named ${name}.`;
  }
  if (!rules.kinds.includes(p.kind)) errors.kind = "Pick the agent it runs.";
  if (/[\r\n]/.test(p.role)) errors.role = "Keep the role to one line.";
  if (/[\r\n]/.test(p.voice)) errors.voice = "Keep the voice to one line.";
  const bad = p.paths.find((g) => !globOk(g.trim()));
  if (bad !== undefined) errors.paths = `${bad.trim()} is not a glob Deck can read.`;
  const colour = p.color.trim();
  if (colour !== "" && !(PLAIN_COLOUR.test(colour) && (rules.isColour ?? cssColour)(colour))) errors.color = "Not a CSS colour.";
  return errors;
}

// Whether Go's path.Match reads the glob: every "[" closes, and no "\"
// ends it.
function globOk(g: string): boolean {
  let open = false;
  for (let i = 0; i < g.length; i++) {
    const ch = g[i];
    if (ch === "\\") {
      if (i === g.length - 1) return false;
      i++;
    } else if (ch === "[") open = true;
    else if (ch === "]") open = false;
  }
  return !open;
}

// The globs of a paths field: one per line or comma, trimmed.
export const parsePaths = (text: string): string[] =>
  text
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter((x) => x !== "");

// The profile as saved: trimmed, its paths parsed.
export function cleanProfile(p: Profile): Profile {
  return {
    ...p,
    name: p.name.trim(),
    model: p.model.trim(),
    effort: p.effort.trim(),
    role: p.role.trim(),
    instructions: p.instructions.trim(),
    voice: p.voice.trim(),
    color: p.color.trim(),
    paths: p.paths.map((x) => x.trim()).filter((x) => x !== ""),
  };
}

// Project profiles first, then global ones, each by name.
export function sortProfiles(list: readonly Profile[]): Profile[] {
  return [...list].sort((a, b) => (a.scope === b.scope ? a.name.localeCompare(b.name) : a.scope === "project" ? -1 : 1));
}

// The profiles a Deck keeps, shared by every view of one client: the
// composer's "@" picker and the editor in Settings. null until they load,
// or when the Deck does not serve ListProfiles.
class ProfileList {
  list: Profile[] | null = null;
  private loading: Promise<void> | null = null;
  private readonly subs = new Set<() => void>();

  constructor(private readonly c: DeckClient) {}

  subscribe = (fn: () => void): (() => void) => {
    this.subs.add(fn);
    return () => this.subs.delete(fn);
  };

  get = (): Profile[] | null => this.list;

  load(force = false): Promise<void> {
    if (this.loading !== null && !force) return this.loading;
    this.loading = (async () => {
      const got = (await this.c.listProfiles?.()) ?? null;
      if (got !== null) this.list = sortProfiles(got);
      // A failed load is tried again on the next ask.
      else this.loading = null;
      for (const fn of this.subs) fn();
    })();
    return this.loading;
  }
}

const lists = new WeakMap<DeckClient, ProfileList>();

function listOf(c: DeckClient): ProfileList {
  let l = lists.get(c);
  if (l === undefined) {
    l = new ProfileList(c);
    lists.set(c, l);
  }
  return l;
}

// The client's profiles, loaded once the Deck says it serves them.
export function useProfiles(c: DeckClient | null): { profiles: Profile[] | null; reload: () => Promise<void> } {
  const l = c === null ? null : listOf(c);
  const on = c !== null && serves(c, "ListProfiles");
  const profiles = useSyncExternalStore(l?.subscribe ?? noSub, l?.get ?? none);
  useEffect(() => {
    if (on) void l?.load();
  }, [on, l]);
  return { profiles: on ? profiles : null, reload: () => l?.load(true) ?? Promise.resolve() };
}

const noSub = () => () => {};
const none = (): null => null;

// What the composer's "@" offers beyond the room: the Deck's profiles and
// the team's people, when there are any. mentions says whether the Deck
// reads "@" as an agent's sigil: one that keeps profiles does
// (docs/design/agents-and-mentions.md); an older one reads only "$".
export function useMentionables(
  c: DeckClient,
  team: Team | string | null,
): { profiles: ProfileRef[] | undefined; people: Person[] | undefined; mentions: boolean } {
  const { profiles } = useProfiles(c);
  const mentions = serves(c, "ListProfiles");
  const named = useMemo(() => profiles?.map((p) => ({ name: p.name, kind: p.kind, role: p.role, color: p.color })), [profiles]);
  const mates = typeof team === "object" && team !== null && team.available ? team.teammates : null;
  const people = useMemo(() => mates?.map((m) => ({ name: m.name, role: "teammate" })), [mates]);
  return { profiles: named, people, mentions };
}
