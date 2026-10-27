import type { ModelChoice, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { createContext, type ReactNode, useContext, useEffect, useId, useState } from "react";
import { type DeckClient, serves } from "../client";
import { cn } from "../lib/cn";
import {
  cleanProfile,
  copyProfile,
  effortsFor,
  emptyProfile,
  PROFILE_SWATCHES,
  parsePaths,
  type Profile,
  type ProfileErrors,
  type ProfileScope,
  useProfiles,
  validateProfile,
} from "../profiles";
import { Button } from "./kit/button";
import { Confirm, Dialog, fieldClass } from "./kit/dialog";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { Row, Section } from "./kit/settings";

// Settings > Agents > Profiles (docs/design/agents-and-mentions.md): the
// named agents of the global and the project deck.json, edited through
// ListProfiles, SaveProfile and DeleteProfile. The list reads as the
// roster does, a glyph in its hue, the name and the role, and a row opens
// the profile in a dialog.

// The Deck the editor talks to and the team's people, whose names a profile
// may not take. The shell provides it around Settings.
export const ProfilesDeck = createContext<{ c: DeckClient; teammates: readonly string[] } | null>(null);

const SCOPE_WORD: Record<ProfileScope, string> = { project: "Project", global: "Global" };
const SCOPE_FILE: Record<ProfileScope, string> = {
  project: "the project's deck.json",
  global: "~/.config/deck/deck.json",
};

const hueOf = (p: Pick<Profile, "kind" | "color">): string => p.color.trim() || kindColour(p.kind);

function Glyph({ p, theme, className }: { p: Pick<Profile, "kind" | "color">; theme: Theme | null; className?: string }) {
  return (
    <span aria-hidden className={cn("inline-grid w-5 shrink-0 place-items-center font-mono text-sm leading-none", className)} style={{ color: hueOf(p) }}>
      {kindMark(theme, p.kind) || p.kind.slice(0, 1) || "●"}
    </span>
  );
}

export function ProfilesEditor({ kinds, theme }: { kinds: readonly string[]; theme: Theme | null }) {
  const deck = useContext(ProfilesDeck);
  const c = deck?.c ?? null;
  const { profiles, reload } = useProfiles(c);
  const [editing, setEditing] = useState<{ p: Profile; isNew: boolean } | null>(null);
  const on = c !== null && serves(c, "ListProfiles");
  const canSave = c !== null && serves(c, "SaveProfile");
  const start = () => setEditing({ p: emptyProfile(kinds[0] ?? "", "project"), isNew: true });

  return (
    <Section title="Profiles" className="profiles">
      {!on ? (
        <Row title="Named agents" description="This Deck keeps no profiles. A newer Deck lets you name agents and give each a role." />
      ) : profiles === null ? (
        <Row title="Loading profiles…" />
      ) : (
        <>
          {profiles.length === 0 && (
            <Row
              title="No profiles yet"
              description={
                <>
                  A profile is a named agent with a role. Save one called charles, and{" "}
                  <code className="font-mono text-foreground/80">@charles</code> starts it in any thread.
                </>
              }
            />
          )}
          {profiles.length > 0 && (
            <ul aria-label="Profiles" className="flex flex-col py-1">
              {profiles.map((p) => (
                <li key={`${p.scope}:${p.name}`}>
                  <button
                    type="button"
                    className="profile -mx-2 flex h-11 w-[calc(100%+1rem)] cursor-pointer items-center gap-3 rounded-[10px] px-2 text-left outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring"
                    data-profile={p.name}
                    aria-label={`Edit ${p.name}`}
                    onClick={() => setEditing({ p, isNew: false })}
                  >
                    <Glyph p={p} theme={theme} />
                    <span className="flex min-w-0 flex-1 items-baseline gap-2.5">
                      <span className="profile-name shrink-0 font-medium text-[13px]" style={{ color: hueOf(p) }}>
                        {p.name}
                      </span>
                      <span className="profile-role min-w-0 truncate text-muted-foreground text-xs">{p.role || p.kind}</span>
                    </span>
                    <span className="hidden shrink-0 font-mono text-2xs text-faint @md:inline">{[p.kind, p.model].filter((x) => x !== "").join(" · ")}</span>
                    <span className="profile-scope w-14 shrink-0 text-right text-2xs text-faint">{SCOPE_WORD[p.scope]}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2 py-3">
            <Button variant="outline" size="sm" disabled={!canSave || kinds.length === 0} onClick={start}>
              New profile
            </Button>
            <p className="min-w-0 flex-1 text-pretty text-faint text-xs leading-relaxed">
              Address one with <code className="font-mono text-muted-foreground">@name</code>. A project profile overrides a global one of the same name.
            </p>
          </div>
        </>
      )}
      {editing !== null && c !== null && (
        <ProfileDialog
          key={`${editing.p.scope}:${editing.p.name}`}
          c={c}
          initial={editing.p}
          isNew={editing.isNew}
          kinds={kinds}
          theme={theme}
          others={profiles ?? []}
          teammates={deck?.teammates ?? []}
          onClose={() => setEditing(null)}
          onCopy={(p) => setEditing({ p: copyProfile(p, profiles ?? []), isNew: true })}
          onSaved={() => {
            setEditing(null);
            void reload();
          }}
        />
      )}
    </Section>
  );
}

// One labelled field, its error or hint under it.
function Field({
  id,
  label,
  error,
  hint,
  className,
  children,
}: {
  id: string;
  label: string;
  error?: string | undefined;
  hint?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <label htmlFor={id} className="font-medium text-xs">
        {label}
      </label>
      {children}
      {error !== undefined ? (
        <p id={`${id}-note`} className="field-error text-destructive-foreground text-xs" role="alert">
          {error}
        </p>
      ) : hint !== undefined ? (
        <p id={`${id}-note`} className="text-faint text-xs">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

type Key = keyof ProfileErrors;

// The models a kind offers (Models), asked through a room of this Deck,
// since the Deck lists them per thread; null with no room yet, or from a
// Deck that does not say. The fields then take free text alone.
function useKindModels(c: DeckClient, kind: string): ModelChoice[] | null {
  const sid = c.store.state.sessions[0]?.id ?? null;
  const [models, setModels] = useState<ModelChoice[] | null>(null);
  useEffect(() => {
    setModels(null);
    if (sid === null || kind === "") return;
    let live = true;
    void c
      .listModels(sid, kind)
      .then((m) => live && setModels(m))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [c, sid, kind]);
  return models;
}

function ProfileDialog({
  c,
  initial,
  isNew,
  kinds,
  theme,
  others,
  teammates,
  onClose,
  onCopy,
  onSaved,
}: {
  c: DeckClient;
  initial: Profile;
  isNew: boolean;
  kinds: readonly string[];
  theme: Theme | null;
  others: readonly Profile[];
  teammates: readonly string[];
  onClose: () => void;
  // Opens a new profile from the draft, under a free name.
  onCopy: (p: Profile) => void;
  onSaved: () => void;
}) {
  const id = useId();
  const [p, setP] = useState(initial);
  const [paths, setPaths] = useState(initial.paths.join("\n"));
  // The kind's models and their efforts, offered beside free text.
  const models = useKindModels(c, p.kind);
  const efforts = effortsFor(models, p.model);
  // Errors show for a field once it was left, and for all of them once Save was pressed.
  const [touched, setTouched] = useState<ReadonlySet<Key>>(new Set());
  const [tried, setTried] = useState(false);
  const [saving, setSaving] = useState(false);
  const [refused, setRefused] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const draft: Profile = { ...p, paths: parsePaths(paths) };
  const errors = validateProfile(draft, {
    kinds,
    teammates,
    profiles: others,
    editing: isNew ? null : { name: initial.name, scope: initial.scope },
  });
  const shown = (k: Key): string | undefined => (tried || touched.has(k) ? errors[k] : undefined);
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => {
    setP((cur) => ({ ...cur, [k]: v }));
    setRefused(null);
  };
  const leave = (k: Key) => () => setTouched((cur) => new Set(cur).add(k));
  const aria = (k: Key, note: boolean) => ({
    id: `${id}-${k}`,
    "aria-invalid": shown(k) !== undefined,
    ...(note || shown(k) !== undefined ? { "aria-describedby": `${id}-${k}-note` } : {}),
  });

  const save = async () => {
    setTried(true);
    if (Object.keys(errors).length > 0 || c.saveProfile === undefined) return;
    setSaving(true);
    const out = cleanProfile(draft);
    const r = await c.saveProfile(out);
    // A profile renamed or moved: the old entry goes once the new one is saved.
    if (r.ok && !isNew && (out.name !== initial.name || out.scope !== initial.scope)) await c.deleteProfile?.(initial.name, initial.scope);
    setSaving(false);
    if (r.ok) onSaved();
    else setRefused(r.message);
  };

  const name = p.name.trim();
  const nameHint = name === "" ? "Letters, digits, - and _." : <>Address it as <code className="font-mono">@{name}</code>.</>;

  // The question replaces the dialog, so one modal is open at a time; the
  // draft stays for a Cancel.
  if (deleting) {
    return (
      <Confirm
        title={`Delete ${initial.name}?`}
        description={`Removes it from ${SCOPE_FILE[initial.scope]}. Agents already started from it keep working.`}
        action="Delete"
        onClose={() => setDeleting(false)}
        onConfirm={() =>
          void c.deleteProfile?.(initial.name, initial.scope).then((ok) => {
            if (ok) onSaved();
          })
        }
      />
    );
  }

  return (
    <Dialog
      title={isNew ? "New profile" : `Edit ${initial.name}`}
      description={`Saved in ${SCOPE_FILE[p.scope]}.`}
      onClose={onClose}
      className="profile-dialog w-[560px]"
      footer={
        <>
          {!isNew && c.deleteProfile !== undefined && serves(c, "DeleteProfile") && (
            <Button variant="ghost-muted" size="sm" className="mr-auto hover:text-destructive-foreground" onClick={() => setDeleting(true)}>
              Delete
            </Button>
          )}
          {!isNew && (
            <Button variant="ghost-muted" size="sm" disabled={Object.keys(errors).length > 0} onClick={() => onCopy(cleanProfile(draft))}>
              Duplicate
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={saving} onClick={() => void save()}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-3.5"
        aria-label="Profile"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="flex min-h-7 flex-wrap items-center justify-between gap-x-4 gap-y-2">
          {/* How the roster will show it: glyph, name and role. */}
          <p className="profile-preview flex min-w-0 flex-1 items-baseline gap-2 text-[13px]" aria-label="Preview">
            <Glyph p={p} theme={theme} className="self-center" />
            <span className="shrink-0 font-semibold" style={{ color: hueOf(p) }}>
              {name || "name"}
            </span>
            <span className="min-w-0 truncate text-faint text-xs">{p.role.trim() || "no role"}</span>
          </p>
          {/* Which deck.json keeps it: this project's, or the global one for
              every project. A saved profile moves on Save. */}
          {(isNew || c.deleteProfile !== undefined) && (
            <div className={segmentTrack()} role="radiogroup" aria-label="Keep it for">
              {(["project", "global"] as const).map((s) => (
                <button
                  key={s}
                  type="button"
                  role="radio"
                  aria-checked={p.scope === s}
                  className={segmentItem({ on: p.scope === s })}
                  onClick={() => set("scope", s)}
                >
                  {s === "project" ? "This project" : "Every project"}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-3">
          <Field id={`${id}-name`} label="Name" error={shown("name")} hint={nameHint}>
            <input
              {...aria("name", true)}
              autoFocus
              className={cn(fieldClass, "h-9 font-mono text-[12.5px]")}
              value={p.name}
              placeholder="charles"
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => set("name", e.target.value)}
              onBlur={leave("name")}
            />
          </Field>
          <Field id={`${id}-kind`} label="Runs" error={shown("kind")}>
            <select
              {...aria("kind", false)}
              className={cn(fieldClass, "h-9 cursor-pointer py-0")}
              value={p.kind}
              onChange={(e) => set("kind", e.target.value)}
              onBlur={leave("kind")}
            >
              {!kinds.includes(p.kind) && <option value={p.kind}>{p.kind || "Pick an agent"}</option>}
              {kinds.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </Field>
          <Field id={`${id}-model`} label="Model">
            <input
              id={`${id}-model`}
              className={cn(fieldClass, "h-9 font-mono text-[12.5px] placeholder:font-sans")}
              value={p.model}
              placeholder={`${p.kind || "the kind"}'s default`}
              spellCheck={false}
              list={`${id}-models`}
              onChange={(e) => set("model", e.target.value)}
            />
            <datalist id={`${id}-models`}>
              {(models ?? []).map((m) => (
                <option key={m.id} value={m.id}>
                  {m.label}
                </option>
              ))}
            </datalist>
          </Field>
          <Field id={`${id}-effort`} label="Effort">
            <input
              id={`${id}-effort`}
              className={cn(fieldClass, "h-9 font-mono text-[12.5px] placeholder:font-sans")}
              value={p.effort}
              placeholder="default"
              spellCheck={false}
              list={`${id}-efforts`}
              onChange={(e) => set("effort", e.target.value)}
            />
            <datalist id={`${id}-efforts`}>
              {efforts.map((e) => (
                <option key={e} value={e} />
              ))}
            </datalist>
          </Field>
        </div>
        <Field id={`${id}-role`} label="Role" error={shown("role")} hint="One line. The roster shows it, and the agent's briefing opens with it.">
          <input
            {...aria("role", true)}
            className={cn(fieldClass, "h-9")}
            value={p.role}
            placeholder="reviewer"
            onChange={(e) => set("role", e.target.value)}
            onBlur={leave("role")}
          />
        </Field>
        <Field id={`${id}-instructions`} label="Instructions">
          <textarea
            id={`${id}-instructions`}
            className={cn(fieldClass, "min-h-20 resize-y leading-relaxed")}
            rows={3}
            value={p.instructions}
            placeholder="Standing instructions, added to its briefing."
            onChange={(e) => set("instructions", e.target.value)}
          />
        </Field>
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-3">
          <Field id={`${id}-voice`} label="Voice" error={shown("voice")}>
            <input
              {...aria("voice", false)}
              className={cn(fieldClass, "h-9")}
              value={p.voice}
              placeholder="dry, terse"
              onChange={(e) => set("voice", e.target.value)}
              onBlur={leave("voice")}
            />
          </Field>
          <Field
            id={`${id}-color`}
            label="Colour"
            error={shown("color")}
            hint={
              <span className="profile-swatches flex flex-wrap items-center gap-1.5" role="group" aria-label="Colours">
                {PROFILE_SWATCHES.map((s) => (
                  <button
                    key={s}
                    type="button"
                    className="profile-swatch size-4 cursor-pointer rounded-full ring-1 ring-hairline-strong outline-none focus-visible:ring-2 focus-visible:ring-ring aria-pressed:ring-2 aria-pressed:ring-foreground/70"
                    style={{ background: s }}
                    aria-label={`Colour ${s}`}
                    aria-pressed={p.color.trim() === s}
                    onClick={() => set("color", s)}
                  />
                ))}
                {p.color.trim() !== "" && (
                  <button type="button" className="cursor-pointer text-faint text-xs hover:text-muted-foreground" onClick={() => set("color", "")}>
                    Kind's hue
                  </button>
                )}
              </span>
            }
          >
            <span className="relative flex">
              <input
                {...aria("color", false)}
                className={cn(fieldClass, "h-9 pr-8 font-mono text-[12.5px] placeholder:font-sans")}
                value={p.color}
                placeholder="the kind's hue"
                spellCheck={false}
                onChange={(e) => set("color", e.target.value)}
                onBlur={leave("color")}
              />
              <span aria-hidden className="pointer-events-none absolute top-1/2 right-3 size-3 -translate-y-1/2 rounded-[4px] ring-1 ring-hairline-strong" style={{ background: hueOf(p) }} />
            </span>
          </Field>
        </div>
        <Field id={`${id}-paths`} label="Paths" error={shown("paths")} hint="Globs it keeps to, one per line.">
          <textarea
            {...aria("paths", true)}
            className={cn(fieldClass, "min-h-14 resize-y font-mono text-[12.5px] placeholder:font-sans")}
            rows={2}
            value={paths}
            placeholder={"src/**\ndocs/*.md"}
            spellCheck={false}
            onChange={(e) => {
              setPaths(e.target.value);
              setRefused(null);
            }}
            onBlur={leave("paths")}
          />
        </Field>
        {refused !== null && (
          <p className="profile-refused text-pretty text-destructive-foreground text-xs" role="alert">
            {refused}
          </p>
        )}
        {/* Enter in a field saves, as the footer's Save does. */}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
