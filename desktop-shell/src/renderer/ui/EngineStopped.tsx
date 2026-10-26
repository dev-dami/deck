import { useState } from "react";
import type { EngineStop } from "../../shared/bridge";
import { CircleAlert, RefreshCw } from "./icons";
import { Button } from "./kit/button";

// In place of the thread when the project's engine stopped by itself: what
// happened, its last output in a card, and one way back.
export function EngineStopped({ stop, onRestart }: { stop: EngineStop; onRestart: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="engine-stopped flex min-h-0 flex-1 flex-col items-center justify-center gap-5 overflow-y-auto px-6 pb-[12vh]" role="alert">
      <span aria-hidden className="liquid-clear grid size-12 animate-enter place-items-center rounded-full text-destructive-foreground">
        <CircleAlert className="size-6" weight="duotone" />
      </span>
      <div className="flex max-w-md flex-col items-center gap-1.5 text-center">
        <h2 className="text-balance font-semibold text-foreground text-lg">{stop.message}</h2>
        <p className="text-pretty text-faint text-sm">Restart the engine to carry on in this project.</p>
      </div>
      {stop.stderr.length > 0 && (
        <pre
          className="engine-log max-h-64 w-full max-w-2xl overflow-auto rounded-xl bg-surface-1 px-4 py-3 font-mono text-code text-muted-foreground ring-1 ring-hairline"
          aria-label="The engine's last output"
        >
          {stop.stderr.join("\n")}
        </pre>
      )}
      <Button
        size="lg"
        disabled={busy}
        onClick={() => {
          setBusy(true);
          void onRestart().finally(() => setBusy(false));
        }}
      >
        <RefreshCw className={busy ? "animate-spin" : undefined} />
        {busy ? "Restarting…" : "Restart"}
      </Button>
    </div>
  );
}
