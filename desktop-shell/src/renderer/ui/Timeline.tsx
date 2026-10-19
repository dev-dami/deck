import type { AgentLine, Message, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { groupParts, isNearBottom } from "@web/transcript";
import { useLayoutEffect, useRef, useState } from "react";
import { agentColour, agentKind } from "../agents";
import { DiffView } from "./DiffView";
import { Markdown } from "./Markdown";

function clock(t: string): string {
  const d = new Date(t);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function Body({ m }: { m: Message }) {
  return (
    <>
      {groupParts(m.parts).map((c, i) => {
        switch (c.k) {
          case "text":
            return m.role === "user" ? (
              <p key={i} className="user-text">
                {c.text}
              </p>
            ) : (
              <Markdown key={i} text={c.text} />
            );
          case "reasoning":
            return (
              <details key={i} className="fold reasoning">
                <summary>Thinking</summary>
                <p className="reasoning-text">{c.text}</p>
              </details>
            );
          case "tool": {
            const name = c.call?.name ?? c.result?.name ?? "tool";
            const failed = c.result?.is_error === true;
            return (
              <details key={i} className={`fold tool${failed ? " failed" : ""}`}>
                <summary>
                  <span className="tool-name">{name}</span>
                  <span className="tool-summary">{c.call?.summary ?? ""}</span>
                  <span className="tool-state">{c.result === null ? "running" : failed ? "failed" : "done"}</span>
                </summary>
                {c.result?.output !== undefined && c.result.output !== "" && (
                  <pre className="tool-output">{c.result.output}</pre>
                )}
                {c.result?.diff !== undefined && c.result.diff !== "" && (
                  <DiffView diff={c.result.diff} truncated={c.result.diff_truncated} />
                )}
              </details>
            );
          }
          case "image":
            return (
              <span key={i} className="chip">
                Image: {c.name}
              </span>
            );
        }
      })}
    </>
  );
}

export function MessageView({ m, roster, theme }: { m: Message; roster: AgentLine[]; theme: Theme | null }) {
  if (m.role === "user") {
    return (
      <article className="msg msg-user" data-role="user">
        <header className="msg-head">
          <span className="msg-who">You</span>
          <time>{clock(m.created_at)}</time>
          {m.edited === true && <span className="muted">edited</span>}
        </header>
        <Body m={m} />
      </article>
    );
  }
  if (m.role === "system") {
    return (
      <article className="msg msg-system" data-role="system">
        <Body m={m} />
      </article>
    );
  }
  const agent = m.agent ?? "agent";
  const kind = agentKind(agent, roster);
  const colour = agentColour(agent, kind);
  return (
    <article className="msg msg-agent" data-role="assistant" data-agent={agent} style={{ borderLeftColor: colour }}>
      <header className="msg-head">
        <span className="msg-mark" style={{ color: colour }}>
          {kindMark(theme, kind) || "●"}
        </span>
        <span className="msg-who" style={{ color: colour }}>
          {agent}
        </span>
        <time>{clock(m.created_at)}</time>
        {!m.finished && <span className="streaming" aria-label="writing" />}
      </header>
      <Body m={m} />
    </article>
  );
}

export function Timeline({
  messages,
  roster,
  theme,
  more,
  onOlder,
}: {
  messages: Message[];
  roster: AgentLine[];
  theme: Theme | null;
  more: boolean;
  onOlder: () => Promise<unknown>;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  // Distance from the bottom before an older page loads, so the view stays put.
  const keep = useRef<number | null>(null);
  const [loading, setLoading] = useState(false);

  useLayoutEffect(() => {
    const el = ref.current;
    if (el === null) return;
    if (keep.current !== null) {
      el.scrollTop = el.scrollHeight - keep.current;
      keep.current = null;
    } else if (stick.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [messages]);

  const older = async () => {
    const el = ref.current;
    if (el === null || loading) return;
    setLoading(true);
    keep.current = el.scrollHeight - el.scrollTop;
    await onOlder();
    setLoading(false);
  };

  return (
    <div
      className="timeline"
      ref={ref}
      onScroll={(e) => {
        stick.current = isNearBottom(e.currentTarget);
      }}
    >
      <div className="timeline-inner">
        {more && (
          <button type="button" className="older" onClick={() => void older()} disabled={loading}>
            {loading ? "Loading…" : "Load older messages"}
          </button>
        )}
        {messages.length === 0 && <p className="empty">No messages yet. Write to an agent below.</p>}
        {messages.map((m) => (
          <MessageView key={m.id} m={m} roster={roster} theme={theme} />
        ))}
      </div>
    </div>
  );
}
