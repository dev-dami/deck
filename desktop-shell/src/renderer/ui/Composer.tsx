import { canSend } from "@web/composer";
import {
  blockedVideo,
  checkImage,
  checkVideo,
  defaultLabel,
  displayName,
  isVideo,
  MAX_VIDEO_BYTES_LOCAL,
  type Outgoing,
  readAsBase64,
  type Source,
  VIDEO_TYPES,
  type VideoGate,
} from "@web/images";
import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { ArrowUp, Paperclip, Square } from "./icons";
import { type KeyboardEvent, type ReactNode, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  applySuggestion,
  fileLister,
  fileSuggestions,
  FILES_LIMIT,
  matchPaths,
  type Mention,
  mergeFiles,
  type PickerType,
  pickerAt,
  retiredForm,
  slashAt,
  slashSuggestions,
  suggestionHue,
} from "../completion";
import { loadHistory, pushHistory, walk } from "../history";
import { cn } from "../lib/cn";
import type { SendKey } from "../prefs";
import { sendAction, sendTitle } from "../queue";
import { parseSlash, type Slash, slashCommand } from "../slash";
import { isMac } from "./keys";
import { AttachmentChip } from "./Attachment";
import { Button } from "./kit/button";

// What the popup over the prompt completes: an agent or a person ("@"), an
// agent ("$"), a file ("#", or "@" before a path) or a slash command ("/",
// at the start of the message).
type Picker = { type: PickerType | "/"; m: Mention };

const PICKER_LABEL = { "@": "Agents and people", $: "Agents", "#": "Files", "/": "Commands" } as const;

// A glass material drawn as a layer behind its parent's content, not on the
// parent: an element with a backdrop-filter becomes the containing block of
// fixed descendants and clips the blur of nested menus, so the composer's
// pickers and dialogs would break on it. The parent is `relative isolate`.
export function Glass({ className }: { className: string }) {
  return <div aria-hidden className={cn("pointer-events-none absolute inset-0 -z-10 rounded-[inherit]", className)} />;
}

// An attached image or video and the blob URL of its preview.
interface Attached {
  image: Outgoing;
  url: string;
}

// No gate: a Deck that takes no video.
const NO_VIDEO: VideoGate = { available: false, ok: false, reason: "This Deck takes no video.", frames: false, framesReason: "This Deck takes no video." };

// The path of a file the person attached, where the window can tell it
// (the desktop app's bridge); "" in a plain page.
function pathOf(f: File): string {
  const deck = (window as { deck?: { filePath?: (f: File) => string } }).deck;
  try {
    return deck?.filePath?.(f) ?? "";
  } catch {
    return "";
  }
}

// The composer: a capsule of glass that floats over the timeline
// (docs/design-glass.md, section 4). A notice and a pending approval stack
// above it; inside, the prompt, then one bar: the room's strip (the mode and
// the agents) leading, and the toolbar, attach and send (stop, while agents
// work and nothing is typed) trailing.
export function Composer({
  draft,
  setDraft,
  images: imagesOn,
  video = NO_VIDEO,
  videoMax = MAX_VIDEO_BYTES_LOCAL,
  online,
  onSend,
  onSteer,
  onSlash,
  onError,
  inputRef,
  toolbar,
  banner,
  above,
  notice,
  strip,
  busy = false,
  onStop,
  paths,
  listFiles,
  history,
  placeholder,
  sendKey,
  className,
  theme = null,
}: {
  draft: string;
  setDraft: (s: string) => void;
  images: boolean;
  // Whether a video can go to the lead now, and if not, why and whether as
  // frames; videoMax is the largest video the Deck takes.
  video?: VideoGate;
  videoMax?: number;
  online: boolean;
  onSend: (text: string, images: Outgoing[]) => Promise<boolean>;
  // Sends into a busy agent's running turn instead of its queue.
  onSteer?: (text: string, images: Outgoing[]) => Promise<boolean>;
  // Runs a slash command; without it, slash text is a message.
  onSlash?: (s: Slash) => Promise<boolean>;
  onError: (text: string) => void;
  inputRef: React.RefObject<HTMLTextAreaElement | null>;
  // At the bar's trailing end, before attach and send.
  toolbar?: ReactNode;
  // A card stacked over the capsule: the thread's pending approval.
  banner?: ReactNode;
  // A card of its own over the composer: the race builder (/race).
  above?: ReactNode;
  // A line above the prompt: an edit in progress, or the take-over after a stop.
  notice?: ReactNode;
  strip?: ReactNode;
  busy?: boolean;
  onStop?: () => void;
  // The room's changed files, for "#".
  paths?: readonly string[];
  // The files of the room's tree that match a query (ListFiles), for "#"
  // after the changed ones. A stable function: a new one asks again.
  listFiles?: ((query: string) => Promise<readonly string[] | null>) | undefined;
  // Where ↑ finds what the person sent: the project's source and the room.
  history?: { project: string; room: string } | null;
  placeholder?: string;
  sendKey: SendKey;
  className?: string;
  // For the agents' glyphs in the pickers.
  theme?: Theme | null;
}) {
  const [images, setImages] = useState<Attached[]>([]);
  const [picker, setPicker] = useState<Picker | null>(null);
  const [pick, setPick] = useState(0);
  const [sending, setSending] = useState(false);
  const [dragging, setDragging] = useState(false);
  const composing = useRef(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const mac = useMemo(isMac, []);
  // The history entry the prompt shows; -1 while the person writes.
  const walkAt = useRef(-1);

  // Thumbnails hold blob URLs until they go.
  const shown = useRef(images);
  shown.current = images;
  useEffect(
    () => () => {
      for (const im of shown.current) URL.revokeObjectURL(im.url);
    },
    [],
  );
  const dropImages = (keep: (im: Attached, i: number) => boolean) =>
    setImages((cur) =>
      cur.filter((im, i) => {
        if (keep(im, i)) return true;
        URL.revokeObjectURL(im.url);
        return false;
      }),
    );

  // The tree's files for the last query Deck answered.
  const [found, setFound] = useState<{ query: string; paths: readonly string[] } | null>(null);
  const lister = useMemo(() => (listFiles === undefined ? null : fileLister(listFiles, (query, p) => setFound({ query, paths: p }))), [listFiles]);
  useEffect(() => () => lister?.stop(), [lister]);

  const options = useMemo(() => {
    if (picker === null) return [];
    if (picker.type === "/") return slashSuggestions(picker.m.query);
    if (picker.type === "#") {
      const q = picker.m.query;
      if (lister === null) return fileSuggestions(q, paths ?? []);
      // While Deck looks, the last answer narrows to what is typed.
      const tree = found === null ? [] : found.query === q ? found.paths : matchPaths(q, found.paths, FILES_LIMIT);
      return mergeFiles(q, paths ?? [], tree);
    }
    return [];
  }, [picker, paths, lister, found]);

  // The prompt grows with its text up to a cap, then scrolls.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (el === null) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 300)}px`;
  }, [draft, inputRef]);

  const track = (el: HTMLTextAreaElement) => {
    const text = el.value;
    const caret = el.selectionStart;
    const slash = onSlash === undefined ? null : slashAt(text, caret);
    const next: Picker | null = slash !== null ? { type: "/", m: slash } : pickerAt(text, caret, (paths ?? []).length > 0 || lister !== null);
    if (next?.type === "#") lister?.ask(next.m.query);
    setPicker(next);
    setPick(0);
  };

  // Where the caret goes once the text the composer just set is in the box.
  // It moves as React writes that text, not a frame later: a key typed in
  // between would land before the caret and split the word.
  const caretAt = useRef<number | null>(null);
  useLayoutEffect(() => {
    const at = caretAt.current;
    const el = inputRef.current;
    if (at === null || el === null) return;
    caretAt.current = null;
    el.focus();
    el.setSelectionRange(at, at);
  });
  const caretTo = (text: string, at: number) => {
    const el = inputRef.current;
    if (el === null) return;
    // The same text makes no new render, so the caret moves now.
    if (el.value === text) {
      el.focus();
      el.setSelectionRange(at, at);
    } else caretAt.current = at;
  };

  const sendable = canSend(draft, images.length);
  const retired = useMemo(() => retiredForm(draft), [draft]);
  // A video the lead cannot watch goes only as frames.
  const blocked = blockedVideo(
    images.map((im) => im.image),
    video,
  );
  const ready = online && !sending && sendable && !blocked;
  const stopping = busy && onStop !== undefined && !sendable;

  const clear = () => {
    setDraft("");
    dropImages(() => false);
    setPicker(null);
    walkAt.current = -1;
  };

  const submit = async (action: "send" | "queue" | "steer", text: string = draft) => {
    if (sending) return;
    const slash = onSlash === undefined ? null : parseSlash(text);
    if (slash !== null && onSlash !== undefined) {
      if ("error" in slash) {
        onError(slash.error);
        return;
      }
      if (slash.cmd === "clear") {
        clear();
        return;
      }
      setSending(true);
      const ok = await onSlash(slash);
      setSending(false);
      if (ok) {
        setDraft("");
        setPicker(null);
      }
      return;
    }
    if (!canSend(text, images.length)) return;
    if (blocked) {
      onError(`${video.reason}: remove the video or send it as frames.`);
      return;
    }
    setSending(true);
    const go = action === "steer" && onSteer !== undefined ? onSteer : onSend;
    const ok = await go(
      text,
      images.map((im) => im.image),
    );
    setSending(false);
    if (ok) {
      if (history !== undefined && history !== null) pushHistory(history.project, history.room, text);
      clear();
    }
  };

  const accept = (i: number) => {
    const s = options[i];
    if (s === undefined || picker === null) return;
    // A command that takes nothing runs as it is picked, and so does one
    // that opens a form of its own (/race).
    const cmd = picker.type === "/" ? slashCommand(s.insert.slice(1)) : undefined;
    if (cmd !== undefined && (cmd.args === "" || cmd.instant === true)) {
      setPicker(null);
      void submit("send", s.insert);
      return;
    }
    const next = applySuggestion(draft, picker.m, s);
    setDraft(next.text);
    setPicker(null);
    caretTo(next.text, next.caret);
  };

  // ↑ in an empty prompt walks back through what the person sent here; ↓
  // walks forward. Typing ends the walk.
  const recall = (dir: "up" | "down"): boolean => {
    if (history === undefined || history === null) return false;
    const walking = walkAt.current >= 0;
    if (!walking && (dir === "down" || draft !== "")) return false;
    const step = walk(loadHistory(history.project, history.room), walkAt.current, dir);
    if (step === null) return walking;
    walkAt.current = step.at;
    setDraft(step.text);
    caretTo(step.text, step.text.length);
    return true;
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (composing.current || e.nativeEvent.isComposing) return;
    if (options.length > 0) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const step = e.key === "ArrowDown" ? 1 : -1;
        setPick((p) => (p + step + options.length) % options.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        accept(pick);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setPicker(null);
        return;
      }
    }
    const mod = e.metaKey || e.ctrlKey;
    if ((e.key === "ArrowUp" || e.key === "ArrowDown") && !mod && !e.shiftKey && !e.altKey) {
      if (recall(e.key === "ArrowUp" ? "up" : "down")) e.preventDefault();
      return;
    }
    if (e.key !== "Enter") return;
    const action = sendAction(sendKey, { mod, shift: e.shiftKey }, busy);
    if (action === "newline") return;
    e.preventDefault();
    void submit(action);
  };

  const videoOn = video.available;
  // Images, and with a Deck that takes video one video per message, each
  // labelled as it lands ("Screenshot 1" pasted, a file by its name).
  const addFiles = async (all: File[], source: Source) => {
    const files = all.filter((f) => f.type.startsWith("image/") || isVideo(f.type));
    if (files.length === 0) return;
    let used = images.reduce((n, im) => n + (im.image.data_b64?.length ?? 0), 0);
    let videos = images.filter((im) => isVideo(im.image.mime)).length;
    const taken = images.map((im) => im.image.label ?? "");
    const added: Attached[] = [];
    for (const f of files) {
      if (isVideo(f.type)) {
        const check = !videoOn ? { ok: false as const, reason: NO_VIDEO.reason } : checkVideo(f, videoMax);
        if (!check.ok) {
          onError(check.reason);
          continue;
        }
        if (videos > 0) {
          onError("One video per message.");
          continue;
        }
        videos++;
        const label = defaultLabel("video", source, f.name, taken);
        taken.push(label);
        const path = pathOf(f);
        added.push({
          image: { name: displayName(f.name || "video.mp4"), mime: f.type, label, file: f, ...(path !== "" ? { path } : {}) },
          url: URL.createObjectURL(f),
        });
        continue;
      }
      const check = checkImage(f, used);
      if (!check.ok) {
        onError(check.reason);
        continue;
      }
      const data = await readAsBase64(f);
      used += data.length;
      const label = defaultLabel("image", source, f.name, taken);
      taken.push(label);
      added.push({
        image: { name: displayName(f.name || "pasted.png"), mime: f.type, label, data_b64: data },
        url: URL.createObjectURL(f),
      });
    }
    setImages((cur) => [...cur, ...added]);
  };
  const change = (i: number, patch: Partial<Outgoing>) =>
    setImages((cur) => cur.map((im, j) => (j === i ? { ...im, image: { ...im.image, ...patch } } : im)));

  return (
    <div className={cn("composer relative mx-auto flex w-full max-w-(--chat-max) flex-col gap-2", className)} data-yolo-region="">
      {picker !== null && options.length > 0 && (
        <ul
          className="suggest material absolute right-0 bottom-full left-0 z-40 mb-2 max-h-80 animate-enter overflow-y-auto rounded-[18px] p-1.5"
          role="listbox"
          aria-label={PICKER_LABEL[picker.type]}
        >
          {options.map((s, i) => (
            <li
              key={s.insert}
              role="option"
              aria-selected={i === pick}
              ref={i === pick ? (el) => el?.scrollIntoView({ block: "nearest" }) : undefined}
              className={cn("flex h-8 cursor-pointer items-center gap-3 rounded-[10px] px-2.5 text-sm", i === pick && "on")}
              onMouseEnter={() => setPick(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                accept(i);
              }}
            >
              {picker.type !== "#" && picker.type !== "/" && (
                // The kind's glyph in its hue; a person wears a teammate's mark.
                <span
                  aria-hidden
                  className="suggest-mark grid w-4 shrink-0 place-items-center font-mono text-[12px] leading-none"
                  style={{ color: suggestionHue(s, kindColour) }}
                >
                  {s.kind !== null ? kindMark(theme, s.kind) || "●" : s.label === "all" ? "∗" : "◇"}
                </span>
              )}
              <span
                // text-code would also set the colour: --color-code is a fill.
                className="suggest-insert min-w-0 truncate font-medium font-mono text-(length:--text-code) text-foreground"
                style={{ color: suggestionHue(s, kindColour) }}
              >
                {picker.type === "$" ? s.insert : s.label}
              </span>
              <span className="suggest-detail ml-auto min-w-0 shrink truncate text-muted-foreground text-xs">{s.detail}</span>
            </li>
          ))}
        </ul>
      )}
      {above !== undefined && above !== null && above !== false && (
        <div className="composer-above relative isolate animate-enter rounded-[20px]">
          <Glass className="liquid-thick" />
          {above}
        </div>
      )}
      {notice}
      {retired !== null && (
        // The typed forms Deck no longer reads go as text; say so, gently.
        <p className="composer-retired px-4 text-faint text-xs" role="status">
          {retired}
        </p>
      )}
      {banner !== undefined && banner !== null && banner !== false && (
        // A pending approval rides on the composer, a card of thicker glass
        // stacked over the capsule.
        <div className="composer-banner relative isolate animate-enter rounded-[20px] p-1" data-composer-banner="">
          <Glass className="liquid-thick" />
          {banner}
        </div>
      )}
      <div
        className={cn(
          // The capsule: regular glass over the timeline. On focus its rim
          // takes the accent.
          "composer-box relative isolate z-10 rounded-[22px]",
          "focus-within:[--glass-rim:color-mix(in_srgb,var(--primary)_60%,transparent)]",
          dragging && "[--glass-rim:var(--primary)]",
        )}
        onDragOver={(e) => {
          if (!imagesOn) return;
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          setDragging(false);
          if (!imagesOn) return;
          e.preventDefault();
          void addFiles([...e.dataTransfer.files], "file");
        }}
      >
        <Glass className="liquid" />
        {images.length > 0 && (
          <div className="attachments flex flex-wrap gap-2 px-3.5 pt-3.5" aria-label="Attachments">
            {images.map((im, i) => (
              <AttachmentChip
                key={im.url}
                att={im.image}
                url={im.url}
                gate={video}
                onRename={(label) => change(i, { label })}
                onRemove={() => dropImages((_, j) => j !== i)}
                onFrames={(on) => change(i, { frames: on })}
              />
            ))}
          </div>
        )}
        <textarea
          ref={inputRef}
          aria-label="Message"
          placeholder={
            online ? (placeholder ?? "Ask for changes, send follow-ups, or attach images") : "Offline. Reconnecting…"
          }
          value={draft}
          rows={2}
          className="block max-h-[min(300px,38vh)] min-h-[3.25rem] w-full resize-none bg-transparent px-[18px] pt-3.5 pb-1 text-[14px] text-foreground leading-[1.55] outline-none placeholder:text-faint"
          onChange={(e) => {
            walkAt.current = -1;
            setDraft(e.target.value);
            track(e.target);
          }}
          onSelect={(e) => track(e.currentTarget)}
          onKeyDown={onKeyDown}
          onPaste={(e) => {
            if (!imagesOn) return;
            const files = [...e.clipboardData.files];
            if (!files.some((f) => f.type.startsWith("image/") || isVideo(f.type))) return;
            e.preventDefault();
            void addFiles(files, "paste");
          }}
          onCompositionStart={() => {
            composing.current = true;
          }}
          onCompositionEnd={() => {
            composing.current = false;
          }}
          onBlur={() => setPicker(null)}
        />
        {/* One bar under the text: the room's strip (the mode and the
            agents) leading; the toolbar (the branch and the menu), attach
            and send trailing. */}
        <div className="composer-bar @container/bar flex items-end gap-2 pr-2 pb-2 pl-2.5">
          <div className="flex min-h-8 min-w-0 flex-1 flex-wrap items-center gap-x-1 gap-y-1">
            {strip !== undefined && <div className="composer-strip contents">{strip}</div>}
          </div>
          <div className="flex min-h-8 min-w-0 shrink-0 items-center gap-0.5">
            {toolbar}
            {imagesOn && (
              <>
                <Button
                  variant="ghost-muted"
                  size="icon"
                  aria-label="Attach images"
                  title={!videoOn ? "Attach images" : video.ok ? "Attach images or a video" : `Attach images (${video.reason}${video.frames ? "; a video goes as frames" : ""})`}
                  onClick={() => fileRef.current?.click()}
                >
                  <Paperclip className="size-[17px]" />
                </Button>
                <input
                  ref={fileRef}
                  type="file"
                  accept={videoOn ? `image/*,${[...VIDEO_TYPES].join(",")}` : "image/*"}
                  multiple
                  hidden
                  onChange={(e) => {
                    void addFiles([...(e.currentTarget.files ?? [])], "file");
                    e.currentTarget.value = "";
                  }}
                />
              </>
            )}
            {stopping ? (
              <Button
                variant="ghost"
                size="icon"
                // Stop is ink, not the accent: it halts work.
                className="stop-all ml-0.5 bg-foreground text-background hover:bg-foreground/85"
                aria-label="Stop all agents"
                title="Stop all agents"
                onClick={onStop}
              >
                <Square className="size-3" weight="fill" />
              </Button>
            ) : (
              <Button
                size="icon"
                // The one accent on screen, once there is something to send;
                // until then a quiet disc.
                variant={ready ? "default" : "secondary"}
                className={cn("send ml-0.5", !ready && "text-faint disabled:opacity-100")}
                onClick={() => void submit(busy ? "queue" : "send")}
                disabled={!ready}
                aria-label="Send"
                title={blocked ? `${video.reason}: remove the video or send it as frames` : sendTitle(sendKey, mac, busy && onSteer !== undefined)}
              >
                <ArrowUp className="size-4" weight="bold" />
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
