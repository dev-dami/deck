import type { ComponentChildren } from "preact";
import type { AppState } from "../state";
import { navigate } from "./hooks";

export function LinkDot({ link }: { link: AppState["link"] }) {
  const label = link === "online" ? "Connected" : link === "connecting" ? "Connecting" : "Offline";
  return <span class={`link-dot link-${link}`} role="img" aria-label={label} title={label} />;
}

interface HeaderProps {
  title: string;
  back?: string;
  state: AppState;
  actions?: ComponentChildren;
  subtitle?: string;
}

export function Header({ title, back, state, actions, subtitle }: HeaderProps) {
  return (
    <header class="topbar">
      {back !== undefined ? (
        <button
          type="button"
          class="btn ghost icon"
          aria-label="Back"
          onClick={() => navigate(back)}
        >
          <span aria-hidden="true">&lsaquo;</span>
        </button>
      ) : null}
      <div class="topbar-title">
        <h1>{title}</h1>
        {subtitle !== undefined ? <p class="muted small-text">{subtitle}</p> : null}
      </div>
      <LinkDot link={state.link} />
      <div class="topbar-actions">{actions}</div>
    </header>
  );
}
