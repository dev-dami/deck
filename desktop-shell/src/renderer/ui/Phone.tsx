import { Smartphone } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { DeckBridge, PhoneOutcome, PhoneState } from "../../shared/bridge";
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
      className="size-56 shrink-0 rounded-lg"
    >
      {/* Scanners need dark modules on a light field whatever the theme. */}
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
        <Row
          title={
            <span className="flex items-center gap-2">
              <Smartphone className="size-4" />
              {running ? "Remote access is on" : "Remote access is off"}
            </span>
          }
          description={
            running
              ? `Relay ${phone?.relayUrl ?? ""}. It keeps running after you close Deck, until you stop it.`
              : "Control Deck from the phone app over your Wi-Fi. Both devices must be on the same network."
          }
          control={
            <div className="flex items-center gap-2">
              {running && (
                <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => void act("code", () => bridge.phoneNewCode())}>
                  New QR code
                </Button>
              )}
              <Button size="sm" disabled={busy !== null} onClick={() => void link(null)}>
                {busy === "link" ? "Starting…" : running ? "Share this project" : "Link a phone"}
              </Button>
            </div>
          }
        />
        <Row
          title="Shares"
          description={
            folder === null
              ? "No local project is open, so only projects already shared with phones are available."
              : `${folder} is shared with the phone you link. Phones can read and drive its sessions.`
          }
        />
        {failure !== null && (
          <Row
            title={<span className="text-destructive-foreground">Could not link</span>}
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
          <div className="flex flex-col items-center gap-4 px-4 py-6 sm:flex-row sm:items-start">
            <Qr code={visible} />
            <div className="min-w-0 space-y-3">
              <p className="text-sm">
                Open the Deck app, tap <span className="font-medium">+ Pair</span> and scan this code. It works once and expires at{" "}
                {clock(visible.expiresAt)}.
              </p>
              <p className="text-muted-foreground text-xs">Or paste this link into the app. Treat it like a password.</p>
              <code className="block max-h-24 select-all overflow-auto break-all rounded-md bg-code px-2 py-1.5 font-mono text-xs">
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
            <Row
              key={d.id}
              title={d.name === "" ? "Unnamed phone" : d.name}
              description={d.id}
              control={
                <Button
                  size="sm"
                  variant="destructive-outline"
                  disabled={busy !== null}
                  onClick={() => void act("revoke", () => bridge.phoneRevoke(d.id), true)}
                >
                  Disconnect
                </Button>
              }
            />
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
