import { useState } from "react";
import type { EngineStop } from "../../shared/bridge";
import { Button } from "./kit/button";

// In place of the thread when the project's engine stopped by itself.
export function EngineStopped({ stop, onRestart }: { stop: EngineStop; onRestart: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="engine-stopped flex min-h-0 flex-1 flex-col items-center justify-center gap-4 overflow-y-auto px-5 pb-[14vh]" role="alert">
      <h2 className="text-balance text-center font-medium text-xl">{stop.message}</h2>
      {stop.stderr.length > 0 && (
        <pre className="engine-log max-h-64 w-full max-w-2xl overflow-auto rounded-lg border bg-code px-3 py-2 font-mono text-muted-foreground text-xs" aria-label="The engine's last output">
          {stop.stderr.join("\n")}
        </pre>
      )}
      <Button
        disabled={busy}
        onClick={() => {
          setBusy(true);
          void onRestart().finally(() => setBusy(false));
        }}
      >
        {busy ? "Restarting…" : "Restart"}
      </Button>
    </div>
  );
}
