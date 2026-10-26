import { CircleAlert, Loader2, Smartphone } from "./icons";
import { useCallback, useEffect, useRef, useState } from "react";
import type { DeckBridge, PhoneOutcome, PhoneState } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { codeUsable, qrPath, type ShownCode, shownCode } from "../phone";
import { Button } from "./kit/button";
import { Row, Section } from "./kit/settings";

const POLL_MS = 5000;

interface Failure {
  message: string;
  needsRelayDir: boolean;
}

function Qr({ code }: { code: ShownCode }) {
  const side = code.qr.length;
  return (
    <svg
      role="img"
      aria-label="Pairing QR code. Scan it in the Deck phone app."
      viewBox={`0 0 ${side} ${side}`}
      shapeRendering="crispEdges"
      className="block size-48 shrink-0"
    >
      <rect width={side} height={side} fill="#ffffff" />
      <path d={qrPath(code.qr)} fill="#000000" />
    </svg>
  );
}

function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// Linking a phone: the engine's `deck mobile connect`, driven from here. It
// starts the Wi-Fi relay and the background daemon, shares the open folder
// and shows the code the Deck phone app scans.
export function PhonePage({ bridge, folder }: { bridge: DeckBridge; folder: string | null }) {
  const [phone, setPhone] = useState<PhoneState | null>(null);
  const [code, setCode] = useState<ShownCode | null>(null);
  const [failure, setFailure] = useState<Failure | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const working = useRef(false);

  const apply = useCallback((out: PhoneOutcome, keepCode: boolean) => {
    if (!out.ok) {
      setFailure({ message: out.message, needsRelayDir: out.needsRelayDir });
      return;
    }
    setFailure(null);
    setPhone(out.phone);
    const fresh = shownCode(out.phone);
    if (fresh !== null) setCode(fresh);
    else if (!keepCode) setCode(null);
  }, []);

  const act = useCallback(
    async (label: string, run: () => Promise<PhoneOutcome>, keepCode = false) => {
      if (working.current) return;
      working.current = true;
      setBusy(label);
      try {
        apply(await run(), keepCode);
      } catch (e) {
        setFailure({ message: e instanceof Error ? e.message : "The phone link failed.", needsRelayDir: false });
      } finally {
        working.current = false;
        setBusy(null);
      }
    },
    [apply],
  );

  useEffect(() => {
    let live = true;
    const poll = async () => {
      if (working.current) return;
      try {
        const out = await bridge.phoneStatus();
        if (live && !working.current) apply(out, true);
      } catch (e) {
        if (live) setFailure({ message: e instanceof Error ? e.message : "The phone link failed.", needsRelayDir: false });
      }
    };
    void poll();
    const t = setInterval(() => void poll(), POLL_MS);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [bridge, apply]);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  const link = (relayDir: string | null) => act("link", () => bridge.phoneLink(folder, relayDir));
  const chooseRelay = async () => {
    const dir = await bridge.pickFolder();
    if (dir !== null) void link(dir);
  };

  const running = phone?.online === true;
  const visible = code !== null && phone !== null && codeUsable(code, phone.devices, now) ? code : null;

  return (
    <>
      <Section title="Link a phone">
        <div className="flex flex-col gap-3 px-4 py-4 @md:flex-row @md:items-center @md:gap-4">
          <div className="flex min-w-0 flex-1 items-center gap-3.5">
            <span
              aria-hidden
              className={cn(
                "relative grid size-11 shrink-0 place-items-center rounded-full ring-1",
                running ? "bg-success/12 text-success-foreground ring-success/25" : "bg-hover text-muted-foreground ring-hairline",
              )}
            >
              <Smartphone className="size-5" weight="duotone" />
              <span
                className={cn(
                  "absolute right-0 bottom-0 size-3 rounded-full ring-2 ring-surface-1",
                  running ? "bg-success" : "bg-faint",
                )}
              />
            </span>
            <div className="min-w-0 space-y-0.5">
              <h3 className="font-medium text-sm">{running ? "Remote access is on" : "Remote access is off"}</h3>
              <p className="text-pretty text-muted-foreground text-xs leading-relaxed">
                {running ? (
                  <>
                    Relay <span className="font-mono text-foreground/80">{phone?.relayUrl ?? ""}</span>. It keeps running after you
                    close Deck, until you stop it.
                  </>
                ) : (
                  "Control Deck from the phone app over your Wi-Fi. Both devices must be on the same network."
                )}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {running && (
              <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => void act("code", () => bridge.phoneNewCode())}>
                New QR code
              </Button>
            )}
            <Button size="sm" disabled={busy !== null} onClick={() => void link(null)}>
              {busy === "link" && <Loader2 className="animate-spin" />}
              {busy === "link" ? "Starting…" : running ? "Share this project" : "Link a phone"}
            </Button>
          </div>
        </div>
        <Row
          title="Shares"
          description={
            folder === null ? (
              "No local project is open, so only projects already shared with phones are available."
            ) : (
              <>
                <span className="font-mono text-foreground/80">{folder}</span> is shared with the phone you link. Phones can read
                and drive its sessions.
              </>
            )
          }
        />
        {failure !== null && (
          <Row
            title={
              <span className="flex items-center gap-1.5 text-destructive-foreground">
                <CircleAlert aria-hidden weight="fill" className="size-3.5" />
                Could not link
              </span>
            }
            description={failure.message}
            control={
              failure.needsRelayDir ? (
                <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => void chooseRelay()}>
                  Choose relay folder…
                </Button>
              ) : undefined
            }
          />
        )}
        {phone?.message != null && failure === null && <Row title="Status" description={phone.message} />}
      </Section>

      {visible !== null && (
        <Section title="Scan with the Deck phone app">
          <div className="flex flex-col items-center gap-5 p-4 @md:flex-row @md:items-start">
            {/* Scanners need dark modules on a light field whatever the theme:
                the code sits on white, framed like a card held up to the phone. */}
            <span className="shrink-0 animate-enter rounded-[18px] bg-white p-3 shadow-[0_1px_2px_rgb(0_0_0/0.1),0_12px_28px_-12px_rgb(0_0_0/0.35)] ring-1 ring-black/5">
              <Qr code={visible} />
            </span>
            <div className="flex min-w-0 flex-1 flex-col gap-3 pt-1">
              <ol className="flex flex-col gap-2 text-sm">
                <li className="flex gap-2.5">
                  <span aria-hidden className="liquid-clear mt-0.5 grid size-[18px] shrink-0 place-items-center rounded-full text-3xs text-muted-foreground tabular-nums">
                    1
                  </span>
                  <span className="text-pretty">
                    Open the Deck app and tap <span className="font-medium">+ Pair</span>.
                  </span>
                </li>
                <li className="flex gap-2.5">
                  <span aria-hidden className="liquid-clear mt-0.5 grid size-[18px] shrink-0 place-items-center rounded-full text-3xs text-muted-foreground tabular-nums">
                    2
                  </span>
                  <span className="text-pretty">
                    Scan this code. It works once and expires at <span className="tabular-nums">{clock(visible.expiresAt)}</span>.
                  </span>
                </li>
              </ol>
              <p className="text-muted-foreground text-xs">Or paste this link into the app. Treat it like a password.</p>
              <code className="block max-h-24 select-all overflow-auto break-all rounded-[10px] bg-code px-2.5 py-2 font-mono text-2xs text-muted-foreground ring-1 ring-hairline">
                {visible.pairUrl}
              </code>
            </div>
          </div>
        </Section>
      )}

      {phone !== null && (
        <Section title="Paired phones">
          {phone.devices.length === 0 && <Row title="No phones yet" description="Phones you pair appear here." />}
          {phone.devices.map((d) => (
            <div key={d.id} className="flex items-center gap-3 px-4 py-3">
              <span aria-hidden className="grid size-8 shrink-0 place-items-center rounded-full bg-hover text-muted-foreground ring-1 ring-hairline">
                <Smartphone className="size-4" />
              </span>
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium text-[13px]">{d.name === "" ? "Unnamed phone" : d.name}</span>
                <span className="truncate font-mono text-2xs text-faint">{d.id}</span>
              </div>
              <Button
                size="sm"
                variant="destructive-outline"
                disabled={busy !== null}
                onClick={() => void act("revoke", () => bridge.phoneRevoke(d.id), true)}
              >
                Disconnect
              </Button>
            </div>
          ))}
        </Section>
      )}

      {running && (
        <Section title="Remote access">
          <Row
            title="Stop remote access"
            description="Stops the relay and the background daemon. Paired phones stay paired and reconnect when you link again."
            control={
              <Button size="sm" variant="destructive-outline" disabled={busy !== null} onClick={() => void act("stop", () => bridge.phoneStop())}>
                Stop
              </Button>
            }
          />
        </Section>
      )}
    </>
  );
}
