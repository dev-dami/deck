import { useState } from "react";
import type { EngineStop } from "../../shared/bridge";
import { CircleAlert, RefreshCw } from "./icons";
import { Button } from "./kit/button";

// In place of the thread when the project's engine stopped by itself: what
// happened, its last output in a card, and one way back.
export function EngineStopped({ stop, onRestart }: { stop: EngineStop; onRestart: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="engine-stopped mx-auto flex min-h-0 w-full max-w-(--chat-max) flex-1 flex-col justify-center gap-4 overflow-y-auto px-6 pb-[12vh]" role="alert">
      <div className="flex flex-col gap-1">
        <p className="flex items-center gap-1.5 font-medium text-destructive-foreground text-xs">
          <CircleAlert aria-hidden className="size-3.5" weight="fill" />
          The engine stopped
        </p>
        <h2 className="text-balance font-semibold text-foreground text-xl">{stop.message}</h2>
        <p className="text-pretty text-muted-foreground text-sm">
          Restart it to carry on in this project.{stop.stderr.length > 0 && " Its last output is below."}
        </p>
      </div>
      {stop.stderr.length > 0 && (
        <pre
          className="engine-log max-h-64 w-full overflow-auto rounded-xl bg-surface-1 px-4 py-3 font-mono text-code text-muted-foreground"
          aria-label="The engine's last output"
        >
          {stop.stderr.join("\n")}
        </pre>
      )}
      <Button
        className="self-start"
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
