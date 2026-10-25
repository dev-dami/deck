import { canSend } from "@web/composer";
import { checkImage, displayName, readAsBase64 } from "@web/images";
import type { AgentLine, Image } from "@web/protocol";
import { kindColour } from "@web/theme";
import { ArrowUp, Paperclip, Square, X } from "lucide-react";
import { type KeyboardEvent, type ReactNode, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  applySuggestion,
  fileAt,
  fileSuggestions,
  type Mention,
  mentionAt,
  slashAt,
  slashSuggestions,
  suggestions,
} from "../completion";
import { loadHistory, pushHistory, walk } from "../history";
import { cn } from "../lib/cn";
import type { SendKey } from "../prefs";
import { sendAction, sendTitle } from "../queue";
import { parseSlash, type Slash, slashCommand } from "../slash";
import { isMac } from "./keys";
import { Button } from "./kit/button";

// What the popup over the prompt completes: an agent ("$"), a changed file
// ("@") or a slash command ("/", at the start of the message).
type Picker = { type: "$" | "@" | "/"; m: Mention };

const PICKER_LABEL = { $: "Agents", "@": "Files", "/": "Commands" } as const;

// An attached image and the blob URL of its thumbnail.
interface Attached {
  image: Image;
  url: string;
}

// The composer, after T3 Code's ChatComposer: an attached banner on top (a
// pending approval), the prompt, a footer with the agent picker and the
// "more" menu on the left and attach and send (stop, while agents work and
// nothing is typed) on the right, and a context strip underneath.
export function Composer({
  draft,
  setDraft,
  roster,
  kinds,
  images: imagesOn,
  online,
  onSend,
  onSteer,
  onSlash,
  onError,
  inputRef,
  toolbar,
  agents,
  banner,
  notice,
  strip,
  busy = false,
  onStop,
  paths,
  history,
  placeholder,
  sendKey,
  className,
}: {
  draft: string;
  setDraft: (s: string) => void;
  roster: AgentLine[];
  kinds: string[];
  images: boolean;
  online: boolean;
  onSend: (text: string, images: Image[]) => Promise<boolean>;
  // Sends into a busy agent's running turn instead of its queue.
  onSteer?: (text: string, images: Image[]) => Promise<boolean>;
  // Runs a slash command; without it, slash text is a message.
  onSlash?: (s: Slash) => Promise<boolean>;
  onError: (text: string) => void;
  inputRef: React.RefObject<HTMLTextAreaElement | null>;
  toolbar?: ReactNode;
  // Who the message goes to, one card per agent, above the prompt.
  agents?: ReactNode;
  banner?: ReactNode;
  // A line above the prompt: an edit in progress, or the take-over after a stop.
  notice?: ReactNode;
  strip?: ReactNode;
  busy?: boolean;
  onStop?: () => void;
  // The room's changed files, for "@".
  paths?: readonly string[];
  // Where ↑ finds what the person sent: the project's source and the room.
  history?: { project: string; room: string } | null;
  placeholder?: string;
  sendKey: SendKey;
  className?: string;
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

  const options = useMemo(() => {
    if (picker === null) return [];
    if (picker.type === "/") return slashSuggestions(picker.m.query);
    if (picker.type === "@") return fileSuggestions(picker.m.query, paths ?? []);
    return suggestions(picker.m.query, roster, kinds).slice(0, 8);
  }, [picker, roster, kinds, paths]);

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
    const file = slash !== null || (paths ?? []).length === 0 ? null : fileAt(text, caret);
    const agent = slash !== null || file !== null ? null : mentionAt(text, caret);
    setPicker(slash !== null ? { type: "/", m: slash } : file !== null ? { type: "@", m: file } : agent !== null ? { type: "$", m: agent } : null);
    setPick(0);
  };

  const caretTo = (at: number) =>
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el === null) return;
      el.focus();
      el.setSelectionRange(at, at);
    });

  const sendable = canSend(draft, images.length);
  const ready = online && !sending && sendable;
  const stopping = busy && onStop !== undefined && !sendable;

  const clear = () => {
    setDraft("");
    dropImages(() => false);
    setPicker(null);
    walkAt.current = -1;
  };

  const submit = async (action: "send" | "queue" | "steer", text: string = draft) => {
    if (sending) return;
    const slash = onSlash === undefined ? null : parseSlash(text, kinds, roster[0]?.kind ?? kinds[0] ?? null);
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
    // A command that takes nothing runs as it is picked.
    if (picker.type === "/" && slashCommand(s.insert.slice(1))?.args === "") {
      setPicker(null);
      void submit("send", s.insert);
      return;
    }
    const next = applySuggestion(draft, picker.m, s);
    setDraft(next.text);
    setPicker(null);
    caretTo(next.caret);
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
    caretTo(step.text.length);
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

  const addFiles = async (all: File[]) => {
    const files = all.filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    let used = images.reduce((n, im) => n + (im.image.data_b64?.length ?? 0), 0);
    const added: Attached[] = [];
    for (const f of files) {
      const check = checkImage(f, used);
      if (!check.ok) {
        onError(check.reason);
        continue;
      }
      const data = await readAsBase64(f);
      used += data.length;
      added.push({
        image: { name: displayName(f.name || "pasted.png"), mime: f.type, data_b64: data },
        url: URL.createObjectURL(f),
      });
    }
    setImages((cur) => [...cur, ...added]);
  };

  return (
    <div className={cn("composer relative mx-auto w-full max-w-(--chat-max)", className)}>
      {picker !== null && options.length > 0 && (
        <ul
          className="suggest absolute right-0 bottom-full left-0 z-40 mb-2 animate-enter overflow-hidden rounded-xl border border-hairline-strong bg-surface-2 p-1 shadow-popover inset-shadow-hl"
          role="listbox"
          aria-label={PICKER_LABEL[picker.type]}
        >
          {options.map((s, i) => (
            <li
              key={s.insert}
              role="option"
              aria-selected={i === pick}
              className={cn("flex cursor-pointer items-center gap-3 rounded-md px-2.5 py-1.5 text-sm", i === pick && "on bg-selected")}
              onMouseEnter={() => setPick(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                accept(i);
              }}
            >
              <span
                className="suggest-insert min-w-0 truncate font-medium font-mono text-xs"
                style={{ color: s.kind === null ? undefined : kindColour(s.kind) }}
              >
                {picker.type === "$" ? s.insert : s.label}
              </span>
              <span className="suggest-detail shrink-0 text-muted-foreground text-xs">{s.detail}</span>
            </li>
          ))}
        </ul>
      )}
      {agents !== undefined && <div className="composer-agents mb-2">{agents}</div>}
      {notice}
      <div
        className={cn(
          // The hero surface: the one place with a ring of amber on focus.
          "composer-box relative z-10 rounded-xl border border-hairline-strong bg-surface-1 inset-shadow-hl transition-[border-color,box-shadow] focus-within:border-primary/50 focus-within:ring-[3px] focus-within:ring-ring",
          dragging && "border-primary/60",
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
          void addFiles([...e.dataTransfer.files]);
        }}
      >
        {banner !== undefined && banner !== null && banner !== false && (
          <div className="border-hairline border-b px-1 pt-1 pb-1" data-composer-banner="">
            {banner}
          </div>
        )}
        {images.length > 0 && (
          <div className="attachments flex flex-wrap gap-2 px-4 pt-3">
            {images.map((im, i) => (
              <span
                key={im.url}
                className="thumb relative size-10 shrink-0 overflow-hidden rounded-md border border-hairline bg-secondary"
                title={im.image.name}
              >
                <img src={im.url} alt={im.image.name} className="size-full object-cover" />
                <button
                  type="button"
                  className="thumb-x absolute top-0.5 right-0.5 grid size-4 cursor-pointer place-items-center rounded-full border border-hairline-strong bg-surface-2 text-muted-foreground hover:text-foreground"
                  aria-label={`Remove ${im.image.name}`}
                  onClick={() => dropImages((_, j) => j !== i)}
                >
                  <X className="size-2.5" />
                </button>
              </span>
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
          className="block max-h-[300px] min-h-[68px] w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-[14px] leading-relaxed outline-none placeholder:text-faint"
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
            if (!files.some((f) => f.type.startsWith("image/"))) return;
            e.preventDefault();
            void addFiles(files);
          }}
          onCompositionStart={() => {
            composing.current = true;
          }}
          onCompositionEnd={() => {
            composing.current = false;
          }}
          onBlur={() => setPicker(null)}
        />
        <div className="flex items-center gap-1 px-2.5 pb-2.5">
          <div className="flex min-w-0 flex-1 items-center gap-0.5">{toolbar}</div>
          <div className="flex shrink-0 items-center gap-1">
            {imagesOn && (
              <>
                <Button
                  variant="ghost-muted"
                  size="icon"
                  className="rounded-full"
                  aria-label="Attach images"
                  title="Attach images"
                  onClick={() => fileRef.current?.click()}
                >
                  <Paperclip />
                </Button>
                <input
                  ref={fileRef}
                  type="file"
                  accept="image/*"
                  multiple
                  hidden
                  onChange={(e) => {
                    void addFiles([...(e.currentTarget.files ?? [])]);
                    e.currentTarget.value = "";
                  }}
                />
              </>
            )}
            {stopping ? (
              <Button
                size="icon"
                className="stop-all rounded-full"
                aria-label="Stop all agents"
                title="Stop all agents"
                onClick={onStop}
              >
                <Square className="size-3 fill-current" />
              </Button>
            ) : (
              <Button
                size="icon"
                // The only amber fill in the app.
                className={cn("send rounded-full border-primary bg-primary text-primary-foreground hover:bg-primary/90", !ready && "opacity-40")}
                onClick={() => void submit(busy ? "queue" : "send")}
                disabled={!ready}
                aria-label="Send"
                title={sendTitle(sendKey, mac, busy && onSteer !== undefined)}
              >
                <ArrowUp className="size-4.5" />
              </Button>
            )}
          </div>
        </div>
      </div>
      {strip !== undefined && (
        <div className="composer-strip relative mx-auto -mt-3 flex w-[calc(100%-2.75rem)] items-center gap-2 rounded-b-xl border border-hairline border-t-0 bg-canvas px-2 pt-4 pb-1">
          {strip}
        </div>
      )}
    </div>
  );
}
