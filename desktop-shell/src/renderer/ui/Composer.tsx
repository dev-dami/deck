import { canSend } from "@web/composer";
import { checkImage, displayName, readAsBase64 } from "@web/images";
import type { AgentLine, Image } from "@web/protocol";
import { kindColour } from "@web/theme";
import { ArrowUp, ImageIcon, Paperclip, Square, X } from "lucide-react";
import { type KeyboardEvent, type ReactNode, useLayoutEffect, useMemo, useRef, useState } from "react";
import { applySuggestion, type Mention, mentionAt, suggestions } from "../completion";
import { cn } from "../lib/cn";
import type { SendKey } from "../prefs";
import { Button } from "./kit/button";

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
  onError,
  inputRef,
  toolbar,
  agents,
  banner,
  strip,
  busy = false,
  onStop,
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
  onError: (text: string) => void;
  inputRef: React.RefObject<HTMLTextAreaElement | null>;
  toolbar?: ReactNode;
  // Who the message goes to, one card per agent, above the prompt.
  agents?: ReactNode;
  banner?: ReactNode;
  strip?: ReactNode;
  busy?: boolean;
  onStop?: () => void;
  placeholder?: string;
  sendKey: SendKey;
  className?: string;
}) {
  const [images, setImages] = useState<Image[]>([]);
  const [mention, setMention] = useState<Mention | null>(null);
  const [pick, setPick] = useState(0);
  const [sending, setSending] = useState(false);
  const [dragging, setDragging] = useState(false);
  const composing = useRef(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const options = useMemo(
    () => (mention === null ? [] : suggestions(mention.query, roster, kinds).slice(0, 8)),
    [mention, roster, kinds],
  );

  // The prompt grows with its text up to a cap, then scrolls.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (el === null) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 300)}px`;
  }, [draft, inputRef]);

  const track = (el: HTMLTextAreaElement) => {
    setMention(mentionAt(el.value, el.selectionStart));
    setPick(0);
  };

  const accept = (i: number) => {
    const s = options[i];
    const el = inputRef.current;
    if (s === undefined || mention === null || el === null) return;
    const next = applySuggestion(draft, mention, s);
    setDraft(next.text);
    setMention(null);
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(next.caret, next.caret);
    });
  };

  const sendable = canSend(draft, images.length);
  const ready = online && !sending && sendable;
  const stopping = busy && onStop !== undefined && !sendable;

  const send = async () => {
    if (sending || !sendable) return;
    setSending(true);
    const ok = await onSend(draft, images);
    setSending(false);
    if (ok) {
      setDraft("");
      setImages([]);
      setMention(null);
    }
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
        setMention(null);
        return;
      }
    }
    if (e.key !== "Enter" || e.shiftKey) return;
    const mod = e.metaKey || e.ctrlKey;
    if (sendKey === "enter" || mod) {
      e.preventDefault();
      void send();
    }
  };

  const addFiles = async (all: File[]) => {
    const files = all.filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    let used = images.reduce((n, im) => n + (im.data_b64?.length ?? 0), 0);
    const added: Image[] = [];
    for (const f of files) {
      const check = checkImage(f, used);
      if (!check.ok) {
        onError(check.reason);
        continue;
      }
      const data = await readAsBase64(f);
      used += data.length;
      added.push({ name: displayName(f.name || "pasted.png"), mime: f.type, data_b64: data });
    }
    setImages((cur) => [...cur, ...added]);
  };

  return (
    <div className={cn("composer relative mx-auto w-full max-w-(--chat-max)", className)}>
      {options.length > 0 && (
        <ul
          className="suggest absolute right-0 bottom-full left-0 z-40 mb-2 animate-enter overflow-hidden rounded-xl border border-hairline-strong bg-surface-2 p-1 shadow-popover inset-shadow-hl"
          role="listbox"
          aria-label="Agents"
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
                className="suggest-insert font-medium font-mono text-xs"
                style={{ color: s.kind === null ? undefined : kindColour(s.kind) }}
              >
                {s.insert}
              </span>
              <span className="suggest-detail text-muted-foreground text-xs">{s.detail}</span>
            </li>
          ))}
        </ul>
      )}
      {agents !== undefined && <div className="composer-agents mb-2">{agents}</div>}
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
          <div className="attachments flex flex-wrap gap-1.5 px-4 pt-3">
            {images.map((im, i) => (
              <span key={i} className="chip inline-flex items-center gap-1.5 rounded-md border border-hairline bg-secondary py-1 pr-1 pl-2 text-xs">
                <ImageIcon className="size-3.5 text-muted-foreground" />
                <span className="max-w-40 truncate">{im.name}</span>
                <button
                  type="button"
                  className="chip-x grid size-4 place-items-center rounded text-muted-foreground hover:bg-hover hover:text-foreground"
                  aria-label={`Remove ${im.name}`}
                  onClick={() => setImages((cur) => cur.filter((_, j) => j !== i))}
                >
                  <X className="size-3" />
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
          onBlur={() => setMention(null)}
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
                onClick={() => void send()}
                disabled={!ready}
                aria-label="Send"
                title={sendKey === "enter" ? "Send (Enter)" : "Send (Ctrl+Enter)"}
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
