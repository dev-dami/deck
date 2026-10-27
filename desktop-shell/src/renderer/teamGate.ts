import { getPrefs, usePrefs } from "./prefs";

// The Team switch (Settings > Agents). Off, the window drops everything of
// the team: the Team page and its footer button, the teammates online,
// rooms teammates share, sharing a room, contracts in the inbox, and the
// named agents (the profiles editor, profiles and people in the "@" picker,
// and the roles and colours members carry from their profiles). Plain
// agents stay. Nothing is deleted: the profiles stay in deck.json and the
// team link in the Deck, so turning it back on shows them all again.

export const teamOn = (): boolean => getPrefs().team;

export const useTeamOn = (): boolean => usePrefs().team;

// v while the team shows, else null: the team as the views read it.
export const gateTeam = <T>(on: boolean, v: T | null): T | null => (on ? v : null);

// What "@" offers beyond the room's members (profiles.ts, useMentionables):
// with the team off, no profiles and no people. Whether the Deck reads "@"
// as an agent's sigil does not change.
export function gateMentionables<P, Q>(
  on: boolean,
  m: { profiles: P | undefined; people: Q | undefined; mentions: boolean },
): { profiles: P | undefined; people: Q | undefined; mentions: boolean } {
  return on ? m : { profiles: undefined, people: undefined, mentions: m.mentions };
}

// A member's colour from its profile, or "" (the kind's hue) with the team off.
export const tintOf = (color: string | undefined, on: boolean = teamOn()): string => (on ? (color ?? "") : "");
