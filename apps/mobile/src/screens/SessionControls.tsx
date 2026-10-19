import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Text, View } from "react-native";
import { decodePasskeyInfo, type PasskeyInfo } from "../../../../pkg/remote/web/src/protocol3";
import type { SessionLike } from "../../../../pkg/remote/web/src/state3";
import { assertNativeAction, registerNativePasskey } from "../native/passkeys";
import { Button, Notice } from "../ui/components";
import { styles } from "../ui/theme";

export function SessionControls({
  session,
  relayUrl,
  pj,
  sessionId,
}: {
  session: SessionLike;
  relayUrl: string;
  pj: string;
  sessionId: string;
}) {
  const [open, setOpen] = useState(false);
  const [keys, setKeys] = useState<PasskeyInfo[]>([]);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const lock = useRef(false);
  const refresh = useCallback(async (): Promise<void> => {
    const result = await session.call<unknown>("passkey.list");
    if (!Array.isArray(result)) throw new Error("Could not read registered passkeys.");
    const parsed = result.map(decodePasskeyInfo);
    if (parsed.some((key) => key === null)) throw new Error("Could not read registered passkeys.");
    setKeys(parsed.filter((key): key is PasskeyInfo => key !== null));
  }, [session]);
  useEffect(() => {
    let active = true;
    if (open)
      void refresh().catch((error: unknown) => {
        if (active) setNotice(error instanceof Error ? error.message : "Could not load settings.");
      });
    return () => {
      active = false;
    };
  }, [refresh, open]);
  const run = async (work: () => Promise<void>): Promise<void> => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setNotice(null);
    try {
      await work();
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "The action failed.");
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  return (
    <View style={{ gap: 12 }}>
      <Button
        title={open ? "Close session settings" : "Session settings & passkeys"}
        onPress={() => setOpen(!open)}
      />
      {open && (
        <View style={styles.card}>
          <Text style={styles.heading}>Session mode</Text>
          <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
            {(["plan", "ask"] as const).map((mode) => (
              <Button
                key={mode}
                title={mode === "plan" ? "Set Plan mode" : "Ask before changes"}
                disabled={busy}
                onPress={() =>
                  void run(async () => {
                    await session.call("mode.set", { session_id: sessionId, mode }, pj);
                    setNotice("Session mode updated.");
                  })
                }
              />
            ))}
          </View>
          {session.welcome.server.allow_yolo && (
            <Button
              title="Enable yolo with passkey"
              disabled={busy || !keys.length}
              onPress={() =>
                Alert.alert(
                  "Allow commands without asking?",
                  "Yolo lets agents run commands and edit files without further approval in this session.",
                  [
                    { text: "Cancel", style: "cancel" },
                    {
                      text: "Verify and enable",
                      style: "destructive",
                      onPress: () =>
                        void run(async () => {
                          const raw = await crypto.subtle.digest(
                            "SHA-256",
                            new TextEncoder().encode(`mode.set|yolo|${pj}|${sessionId}`),
                          );
                          // The action digest is lowercase hex, as specified by the Go gate.
                          const digest = Array.from(new Uint8Array(raw), (byte) =>
                            byte.toString(16).padStart(2, "0"),
                          ).join("");
                          const assertion = await assertNativeAction(session, relayUrl, digest);
                          await session.call(
                            "mode.set",
                            { session_id: sessionId, mode: "yolo", assertion },
                            pj,
                          );
                          setNotice("Yolo enabled for this session.");
                        }),
                    },
                  ],
                )
              }
            />
          )}
          <Text style={styles.heading}>Passkeys ({keys.length}/5)</Text>
          <Text style={styles.secondary}>
            Destructive approvals require verification using a registered passkey and your signed
            app's domain association.
          </Text>
          <Button
            title="Add a passkey"
            disabled={busy || keys.length >= 5}
            onPress={() =>
              void run(async () => {
                await registerNativePasskey(session, relayUrl);
                await refresh();
                setNotice("Passkey registered.");
              })
            }
          />
          {keys.map((key) => (
            <View key={key.id} style={{ gap: 8 }}>
              <Text style={styles.secondary}>
                Added {new Date(key.created_at).toLocaleDateString()}
              </Text>
              <Button
                title="Remove passkey"
                disabled={busy}
                onPress={() =>
                  Alert.alert(
                    "Remove this passkey?",
                    "This passkey will no longer authorize actions on this computer.",
                    [
                      { text: "Cancel", style: "cancel" },
                      {
                        text: "Remove",
                        style: "destructive",
                        onPress: () =>
                          void run(async () => {
                            await session.call("passkey.remove", { id: key.id });
                            await refresh();
                          }),
                      },
                    ],
                  )
                }
              />
            </View>
          ))}
          {notice && <Notice>{notice}</Notice>}
        </View>
      )}
    </View>
  );
}
