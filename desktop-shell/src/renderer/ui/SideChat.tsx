import type { Session, Theme } from "@web/protocol";
import type { AppState } from "@web/state";
import { kindColour, kindMark } from "@web/theme";
import { ArrowUp, Loader2 } from "./icons";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { findSide, sideDigest, sideMessage, sideQuestion, sideTitle } from "../sideChat";
import { Button } from "./kit/button";
import { Glass } from "./Composer";
import { MessageView } from "./Timeline";

// Ask about what the agents of the open thread are doing, without stopping
// or steering them: another agent answers in a side thread of its own, from
// a snapshot of the main thread sent with each question.
export function SideChat({
  c,
  state,
  main,
  api,
  theme,
}: {
  c: DeckClient;
  state: AppState;
  main: Session;
  api: ProjectApi | undefined;
  theme: Theme | null;
}) {
  const kinds = state.server?.kinds ?? [];
  const [kind, setKind] = useState(kinds[0] ?? "claude");
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const side = findSide(state.sessions, main);
  const messages = side === undefined ? [] : (state.messages[side.id] ?? []);
  const list = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (side !== undefined) void api?.loadThread(side.id);
  }, [api, side?.id]);

  useLayoutEffect(() => {
    const el = list.current;
    if (el !== null) el.scrollTop = el.scrollHeight;
  }, [messages]);

  const ask = async () => {
    const q = text.trim();
    if (q === "" || sending) return;
    setSending(true);
    let s = side;
    if (s === undefined) {
      const made = await c.createSession(sideTitle(main));
      if (made === null) {
        setSending(false);
        return;
      }
      s = made;
    }
    // The first question picks the agent; later ones go to it.
    const answered = (state.messages[s.id] ?? []).some((m) => m.role === "assistant");
    const digest = sideDigest(main, state.roster[main.id] ?? [], state.approvals, state.messages[main.id] ?? []);
    const ok = await c.send(s.id, sideMessage(answered ? "" : `$${kind}`, q, digest), []);
    setSending(false);
    if (ok) setText("");
  };

  if (api === undefined) {
    return <p className="px-4 py-16 text-center text-faint text-sm">The side chat needs a project on this computer.</p>;
  }

  return (
    <section aria-label="Side chat" className="flex min-h-0 flex-1 flex-col">
      <div ref={list} className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
        {messages.length === 0 && (
          <div className="flex flex-col gap-1 px-1 py-8">
            <p className="font-medium text-foreground text-sm">Ask about this thread</p>
            <p className="text-pretty text-faint text-xs leading-relaxed">
              "What is claude-1 doing?", "Why did that test fail?", "Is this approach sound?" Another agent answers from
              what the thread shows; the agents at work are not interrupted.
            </p>
          </div>
        )}
        {messages.map((m) =>
          m.role === "user" ? (
            <div key={m.id} className="msg-user flex justify-end" data-role="user">
              <p className="side-question max-w-[85%] whitespace-pre-wrap text-pretty rounded-[18px_18px_6px_18px] bg-bubble px-3.5 py-2 text-sm">
                {sideQuestion(m.parts.map((p) => p.text ?? "").join(""))}
              </p>
            </div>
          ) : (
            <div key={m.id} className="[&_.text-\[15px\]]:text-sm">
              <MessageView m={m} roster={side === undefined ? [] : (state.roster[side.id] ?? [])} theme={theme} />
            </div>
          ),
        )}
      </div>
      <div className="p-3 pt-1">
        {/* The room's composer, smaller: a capsule of glass whose rim takes
            the accent on focus. */}
        <div className="relative isolate rounded-[20px] focus-within:[--glass-rim:color-mix(in_srgb,var(--primary)_60%,transparent)]">
          <Glass className="liquid" />
          <textarea
            aria-label="Side question"
            placeholder="Ask about what the agents are doing…"
            rows={2}
            value={text}
            className="block w-full resize-none bg-transparent px-3.5 pt-3 text-sm outline-none placeholder:text-faint"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void ask();
              }
            }}
          />
          <div className="flex items-center gap-1 pr-2 pb-2 pl-3">
            <span className="text-faint text-xs">Answered by</span>
            <div className="flex items-center gap-0.5" role="radiogroup" aria-label="Agent for the side chat">
              {kinds.map((k) => (
                <button
                  key={k}
                  type="button"
                  role="radio"
                  aria-checked={kind === k}
                  disabled={messages.some((m) => m.role === "assistant")}
                  className={cn(
                    "flex h-6 cursor-pointer items-center gap-1 rounded-full px-2 text-xs outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default",
                    kind === k ? "bg-selected text-foreground" : "text-muted-foreground hover:bg-hover hover:text-foreground",
                  )}
                  onClick={() => setKind(k)}
                >
                  <span className="font-mono" style={{ color: kindColour(k) }}>
                    {kindMark(theme, k) || "●"}
                  </span>
                  {k}
                </button>
              ))}
            </div>
            <Button
              size="icon-sm"
              variant={text.trim() === "" ? "secondary" : "default"}
              className={cn("ml-auto", text.trim() === "" && "text-faint disabled:opacity-100")}
              aria-label="Ask"
              disabled={sending || text.trim() === ""}
              onClick={() => void ask()}
            >
              {sending || side?.busy === true ? <Loader2 className="animate-spin" /> : <ArrowUp weight="bold" />}
            </Button>
          </div>
        </div>
      </div>
    </section>
  );
}
