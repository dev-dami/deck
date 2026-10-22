import { useCallback, useEffect, useState } from "react";
import { Alert, Pressable, ScrollView, Text, View } from "react-native";
import { decodePasskeyInfo, type PasskeyInfo } from "../../../../pkg/remote/web/src/protocol3";
import type { ComputerView, SessionLike } from "../../../../pkg/remote/web/src/state3";
import { registerNativePasskey } from "../native/passkeys";
import { Badge, Button, Dot, Group, Notice, Row, Sheet } from "../ui/components";
import { Icon } from "../ui/icons";
import { useTheme } from "../ui/theme";
import { buildTheme, PALETTES } from "../ui/themes";

const MAX_PASSKEYS = 5;

export function SettingsSheet({
  visible,
  onClose,
  session,
  relayUrl,
  computers,
  onOpenComputers,
}: {
  visible: boolean;
  onClose(): void;
  session: SessionLike | null;
  relayUrl: string | null;
  computers: number;
  onOpenComputers(): void;
}) {
  const { t, s, themeId, setTheme } = useTheme();
  const [pane, setPane] = useState<"main" | "theme">("main");
  const [keys, setKeys] = useState<PasskeyInfo[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const refresh = useCallback(async (): Promise<void> => {
    if (session === null) return;
    const result = await session.call<unknown>("passkey.list");
    if (!Array.isArray(result)) throw new Error("Could not read your passkeys.");
    const parsed = result.map(decodePasskeyInfo);
    if (parsed.some((k) => k === null)) throw new Error("Could not read your passkeys.");
    setKeys(parsed.filter((k): k is PasskeyInfo => k !== null));
  }, [session]);

  useEffect(() => {
    let active = true;
    if (!visible) {
      setPane("main");
      return;
    }
    setNotice(null);
    void refresh().catch((error: unknown) => {
      if (active) setNotice(error instanceof Error ? error.message : "Could not load passkeys.");
    });
    return () => {
      active = false;
    };
  }, [visible, refresh]);

  async function run(work: () => Promise<void>): Promise<void> {
    if (busy) return;
    setBusy(true);
    setNotice(null);
    try {
      await work();
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "The action failed.");
    } finally {
      setBusy(false);
    }
  }

  const current = PALETTES.find((p) => p.id === themeId);

  if (pane === "theme")
    return (
      <Sheet
        visible={visible}
        onClose={onClose}
        title="Theme"
        subtitle="Matches Deck CLI and desktop."
      >
        <ScrollView
          style={{ flexGrow: 0 }}
          contentContainerStyle={{
            flexDirection: "row",
            flexWrap: "wrap",
            gap: 12,
            paddingBottom: 4,
          }}
        >
          {PALETTES.map((p) => {
            const preview = buildTheme(p);
            const on = p.id === themeId;
            return (
              <Pressable
                key={p.id}
                accessibilityRole="radio"
                accessibilityState={{ checked: on }}
                accessibilityLabel={`${p.name} theme`}
                onPress={() => setTheme(p.id)}
                style={{
                  width: "47%",
                  flexGrow: 1,
                  padding: 8,
                  gap: 8,
                  borderRadius: 18,
                  boxShadow: `0 0 0 ${on ? 2 : 1}px ${on ? t.primary : t.line}`,
                }}
              >
                <View
                  style={{
                    height: 74,
                    borderRadius: 12,
                    padding: 10,
                    gap: 6,
                    backgroundColor: p.bg,
                  }}
                >
                  <View
                    style={{
                      height: 22,
                      borderRadius: 7,
                      paddingHorizontal: 7,
                      flexDirection: "row",
                      alignItems: "center",
                      backgroundColor: preview.card,
                      boxShadow: preview.shadow.card,
                    }}
                  >
                    <View
                      style={{ width: 34, height: 5, borderRadius: 3, backgroundColor: p.fg }}
                    />
                    <View style={{ flex: 1 }} />
                    <Dot color={p.success} size={6} />
                  </View>
                  <View style={{ flexDirection: "row", gap: 5 }}>
                    <View
                      style={{ width: 40, height: 14, borderRadius: 7, backgroundColor: p.primary }}
                    />
                    <View
                      style={{ width: 22, height: 14, borderRadius: 7, backgroundColor: p.warn }}
                    />
                  </View>
                </View>
                <View
                  style={{
                    flexDirection: "row",
                    alignItems: "center",
                    gap: 6,
                    paddingHorizontal: 2,
                  }}
                >
                  <Text
                    numberOfLines={1}
                    style={{ flex: 1, fontSize: 14, fontWeight: "600", color: t.fg }}
                  >
                    {p.name}
                  </Text>
                  <Text style={{ fontSize: 11, fontWeight: "600", color: t.muted }}>
                    {p.source}
                  </Text>
                </View>
              </Pressable>
            );
          })}
        </ScrollView>
        <Button title="Back" icon="back" size="sm" onPress={() => setPane("main")} />
      </Sheet>
    );

  return (
    <Sheet visible={visible} onClose={onClose} title="Settings">
      <ScrollView style={{ flexGrow: 0 }} contentContainerStyle={{ gap: 12, paddingBottom: 4 }}>
        <Group>
          <Row
            title="Theme"
            onPress={() => setPane("theme")}
            leading={
              <View style={{ flexDirection: "row" }}>
                <View
                  style={{
                    width: 18,
                    height: 18,
                    borderRadius: 9,
                    backgroundColor: current?.bg,
                    borderWidth: 2,
                    borderColor: t.line,
                  }}
                />
                <View
                  style={{
                    width: 18,
                    height: 18,
                    borderRadius: 9,
                    marginLeft: -6,
                    backgroundColor: current?.primary,
                    borderWidth: 2,
                    borderColor: t.card,
                  }}
                />
              </View>
            }
            trailing={
              <View style={s.row}>
                <Text style={s.secondary}>{current?.name}</Text>
                <Icon name="chevron-right" color={t.muted} size={15} weight={2.2} />
              </View>
            }
          />
          <Row
            divider
            title="Computers"
            onPress={onOpenComputers}
            leading={<Icon name="laptop" color={t.fg} size={18} weight={1.9} />}
            trailing={
              <View style={s.row}>
                <Text style={s.secondary}>{computers} paired</Text>
                <Icon name="chevron-right" color={t.muted} size={15} weight={2.2} />
              </View>
            }
          />
        </Group>
        <Text style={[s.label, { marginTop: 6 }]}>
          Passkeys{keys ? ` · ${keys.length} of ${MAX_PASSKEYS}` : ""}
        </Text>
        <Text style={[s.small, { marginHorizontal: 4 }]}>
          Destructive approvals and yolo ask for one of these.
        </Text>
        {session === null || relayUrl === null ? (
          <Notice>Connect to a computer to manage its passkeys.</Notice>
        ) : (
          <Group>
            {(keys ?? []).map((key, i) => (
              <Row
                key={key.id}
                divider={i > 0}
                title="Passkey"
                subtitle={`Added ${new Date(key.created_at).toLocaleDateString()}`}
                leading={<Icon name="key" color={t.fg} size={18} />}
                trailing={
                  <Button
                    title="Remove"
                    size="sm"
                    disabled={busy}
                    onPress={() =>
                      Alert.alert(
                        "Remove this passkey?",
                        "It will no longer authorize actions on this computer.",
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
                }
              />
            ))}
            <Row
              divider={(keys ?? []).length > 0}
              title="Add a passkey"
              onPress={
                busy || (keys?.length ?? 0) >= MAX_PASSKEYS
                  ? undefined
                  : () =>
                      void run(async () => {
                        await registerNativePasskey(session, relayUrl);
                        await refresh();
                        setNotice("Passkey added.");
                      })
              }
              leading={<Icon name="plus" color={t.primary} size={18} weight={2.2} />}
            />
          </Group>
        )}
        {notice && <Notice>{notice}</Notice>}
      </ScrollView>
    </Sheet>
  );
}

export function ComputersSheet({
  visible,
  onClose,
  computers,
  current,
  onSelect,
  onPair,
}: {
  visible: boolean;
  onClose(): void;
  computers: ComputerView[];
  current: string | null;
  onSelect(cid: string): void;
  onPair(): void;
}) {
  const { t } = useTheme();
  return (
    <Sheet visible={visible} onClose={onClose} title="Computers">
      <ScrollView style={{ flexGrow: 0 }} contentContainerStyle={{ gap: 12, paddingBottom: 4 }}>
        {computers.length > 0 && (
          <Group>
            {computers.map((c, i) => {
              const color =
                c.status === "online" ? t.success : c.status === "connecting" ? t.warn : t.muted;
              const status =
                c.status === "online"
                  ? `Connected · ${c.inbox.filter((x) => x.state === "needs_you").length} need you`
                  : c.status === "connecting"
                    ? "Connecting"
                    : c.status === "revoked"
                      ? "Access revoked"
                      : c.status === "needs-pairing"
                        ? "Pair again"
                        : "Offline";
              return (
                <Row
                  key={c.record.cid}
                  divider={i > 0}
                  title={c.record.name}
                  subtitle={status}
                  onPress={() => onSelect(c.record.cid)}
                  leading={<Dot color={color} />}
                  trailing={
                    c.record.cid === current ? (
                      <Icon name="check" color={t.primary} size={18} weight={2.4} />
                    ) : c.status === "needs-pairing" || c.status === "revoked" ? (
                      <Badge text="Pair" color={t.warn} />
                    ) : undefined
                  }
                />
              );
            })}
          </Group>
        )}
        <Button title="Pair a computer" icon="plus" onPress={onPair} />
      </ScrollView>
    </Sheet>
  );
}
