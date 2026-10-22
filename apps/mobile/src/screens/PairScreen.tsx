import { CameraView, useCameraPermissions } from "expo-camera";
import { useEffect, useRef, useState } from "react";
import { KeyboardAvoidingView, Platform, ScrollView, Text, TextInput, View } from "react-native";
import { Button, IconButton, LargeTitle, Notice, TopBar } from "../ui/components";
import { previewPairing } from "../ui/pairing";
import { mono, useTheme } from "../ui/theme";

export interface PairScreenProps {
  onPair(link: string, deviceName: string): Promise<void>;
  onBack(): void;
  initialUrl?: string;
}

const COMMAND = "deck mobile pair";

export function PairScreen({ onPair, onBack, initialUrl }: PairScreenProps) {
  const { t, s } = useTheme();
  const [permission, requestPermission] = useCameraPermissions();
  const [scanning, setScanning] = useState(false);
  const [pasting, setPasting] = useState(Boolean(initialUrl));
  const [link, setLink] = useState(initialUrl ?? "");
  const [name, setName] = useState(Platform.OS === "ios" ? "My iPhone" : "My phone");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const locked = useRef(false);
  const scanLocked = useRef(false);
  useEffect(() => {
    if (initialUrl) {
      setLink(initialUrl);
      setPasting(true);
      setScanning(false);
    }
  }, [initialUrl]);

  let preview: ReturnType<typeof previewPairing> | null = null;
  let previewError: string | null = null;
  if (link.trim()) {
    try {
      preview = previewPairing(link);
    } catch (failure) {
      previewError =
        failure instanceof Error ? failure.message : "That isn't a valid pairing link.";
    }
  }

  async function pair(): Promise<void> {
    if (locked.current) return;
    const value = link.trim();
    if (!value || value.length > 16_384 || !name.trim()) {
      setError("Name this phone and scan or paste a pairing link.");
      return;
    }
    locked.current = true;
    setBusy(true);
    setError(null);
    setScanning(false);
    try {
      previewPairing(value);
      await onPair(value, name.trim());
      setLink("");
    } catch (failure) {
      setError(
        failure instanceof Error
          ? failure.message
          : "Pairing failed. Make a fresh code and try again.",
      );
    } finally {
      locked.current = false;
      setBusy(false);
    }
  }

  function openCamera(): void {
    void (async () => {
      const result = permission?.granted ? permission : await requestPermission();
      if (result.granted) {
        scanLocked.current = false;
        setScanning(true);
      } else {
        setPasting(true);
        setError("Camera access is off. Paste the pairing link instead.");
      }
    })().catch(() => setError("Could not open the camera. Paste the pairing link instead."));
  }

  return (
    <KeyboardAvoidingView style={s.screen} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <TopBar left={<IconButton icon="close" label="Close" onPress={onBack} />} />
      <ScrollView
        contentContainerStyle={{ paddingBottom: 40, gap: 16 }}
        keyboardShouldPersistTaps="handled"
      >
        <LargeTitle
          title="Pair a computer"
          subtitle="Two steps. Everything stays end-to-end encrypted."
        />
        <View style={{ paddingHorizontal: 16, gap: 14 }}>
          <View style={[s.card, { padding: 16, gap: 10 }]}>
            <Step n={1} text="Run this on your computer" />
            <View
              style={[s.code, { flexDirection: "row", alignItems: "center", paddingVertical: 12 }]}
            >
              <Text selectable style={{ flex: 1, fontFamily: mono, fontSize: 14, color: t.fg }}>
                {COMMAND}
              </Text>
            </View>
            <Text style={s.small}>The code expires after 5 minutes. Keep the terminal open.</Text>
          </View>

          <View style={[s.card, { padding: 16, gap: 12 }]}>
            <Step n={2} text="Scan the code it shows" />
            {scanning && permission?.granted ? (
              <View style={{ height: 260, borderRadius: 18, overflow: "hidden" }}>
                <CameraView
                  style={{ flex: 1 }}
                  facing="back"
                  barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
                  onBarcodeScanned={({ data }) => {
                    if (scanLocked.current || locked.current) return;
                    scanLocked.current = true;
                    setLink(data);
                    setScanning(false);
                    setError(null);
                  }}
                />
              </View>
            ) : (
              <Button
                title={preview ? "Scan again" : "Open camera"}
                tone={preview ? "secondary" : "primary"}
                size="lg"
                disabled={busy}
                onPress={openCamera}
              />
            )}
            {scanning && (
              <Button title="Stop scanning" size="sm" onPress={() => setScanning(false)} />
            )}
            {!pasting && !scanning && (
              <Button
                title="Paste a pairing link instead"
                tone="ghost"
                size="sm"
                onPress={() => setPasting(true)}
              />
            )}
            {pasting && (
              <TextInput
                accessibilityLabel="Pairing link"
                value={link}
                onChangeText={setLink}
                autoCorrect={false}
                autoCapitalize="none"
                secureTextEntry
                style={s.input}
                placeholderTextColor={t.muted}
                placeholder="https://your-relay/#p=…"
                editable={!busy}
                maxLength={16_384}
              />
            )}
          </View>

          {previewError && <Notice error>{previewError}</Notice>}
          {preview && (
            <View style={[s.card, { padding: 16, gap: 10 }]}>
              <Text style={s.label}>Ready to pair</Text>
              <Text style={[s.heading, { marginHorizontal: 4 }]}>{preview.name}</Text>
              <Text style={[s.small, { marginHorizontal: 4 }]}>Relay · {preview.relay}</Text>
              {preview.localHTTP && (
                <Text style={[s.small, { marginHorizontal: 4 }]}>
                  Local network test: use trusted Wi-Fi. Messages stay end-to-end encrypted; the
                  relay link uses HTTP.
                </Text>
              )}
              <Text style={[s.label, { marginTop: 6 }]}>Name this phone</Text>
              <TextInput
                accessibilityLabel="Phone name"
                value={name}
                onChangeText={setName}
                maxLength={80}
                autoCorrect={false}
                style={s.input}
                placeholderTextColor={t.muted}
                placeholder="My phone"
                editable={!busy}
              />
              <Button
                title={`Pair with ${preview.name}`}
                tone="primary"
                size="lg"
                busy={busy}
                disabled={!name.trim()}
                onPress={() => void pair()}
              />
            </View>
          )}
          {error && <Notice error>{error}</Notice>}
          <Text style={[s.small, { textAlign: "center", marginHorizontal: 12 }]}>
            Treat the code like a password. It lets this phone reach your computer.
          </Text>
        </View>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Step({ n, text }: { n: number; text: string }) {
  const { t, s } = useTheme();
  return (
    <View style={s.row}>
      <View
        style={{
          width: 24,
          height: 24,
          borderRadius: 12,
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: t.chip,
        }}
      >
        <Text style={{ fontSize: 13, fontWeight: "600", color: t.fg }}>{n}</Text>
      </View>
      <Text style={s.heading}>{text}</Text>
    </View>
  );
}
