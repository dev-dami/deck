import { CameraView, useCameraPermissions } from "expo-camera";
import { useEffect, useRef, useState } from "react";
import { KeyboardAvoidingView, Platform, ScrollView, Text, TextInput, View } from "react-native";
import { Button, Notice, ScreenHeader } from "../ui/components";
import { previewPairing } from "../ui/pairing";
import { useTheme } from "../ui/ThemeContext";

export interface PairScreenProps {
  onPair(link: string, deviceName: string): Promise<void>;
  onBack(): void;
  error?: string | null;
  initialUrl?: string;
}

export function PairScreen({ onPair, onBack, error: externalError, initialUrl }: PairScreenProps) {
  const { styles, tokens } = useTheme();
  const [permission, requestPermission] = useCameraPermissions();
  const [scanning, setScanning] = useState(false);
  const [link, setLink] = useState(initialUrl ?? "");
  const [name, setName] = useState(Platform.OS === "ios" ? "My iPhone" : "My phone");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const locked = useRef(false);
  const scanLocked = useRef(false);
  useEffect(() => {
    if (initialUrl) {
      setLink(initialUrl);
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
        failure instanceof Error ? failure.message : "That is not a valid pairing link.";
    }
  }
  async function pair(value: string): Promise<void> {
    if (locked.current) return;
    const trimmed = value.trim();
    if (!trimmed || trimmed.length > 16_384 || !name.trim()) {
      setError("Enter your phone's name and a valid pairing link.");
      return;
    }
    locked.current = true;
    setBusy(true);
    setError(null);
    setScanning(false);
    try {
      previewPairing(trimmed);
      await onPair(trimmed, name.trim());
      setLink("");
    } catch (failure) {
      setError(
        failure instanceof Error
          ? failure.message
          : "Pairing failed. Generate a fresh code and try again.",
      );
    } finally {
      locked.current = false;
      setBusy(false);
    }
  }
  return (
    <KeyboardAvoidingView
      style={styles.screen}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScreenHeader
        title="Pair a computer"
        subtitle="Your phone and computer. A private connection."
        onBack={onBack}
      />
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <View style={styles.card}>
          <Text style={styles.eyebrow}>On your computer</Text>
          <Text style={styles.body}>Open your terminal and run:</Text>
          <Text selectable style={[styles.code, { color: tokens.accent }]}>
            deck mobile pair
          </Text>
          <Text style={styles.secondary}>
            The code expires after 5 minutes. Keep the terminal open while you connect.
          </Text>
        </View>
        <Text accessibilityRole="header" style={styles.heading}>
          Name this phone
        </Text>
        <TextInput
          accessibilityLabel="Phone name"
          value={name}
          onChangeText={setName}
          maxLength={80}
          autoCorrect={false}
          style={styles.input}
          placeholderTextColor={tokens.muted}
          placeholder="My phone"
          editable={!busy}
        />
        {scanning && permission?.granted ? (
          <View
            style={{
              height: 280,
              borderRadius: 18,
              overflow: "hidden",
              borderWidth: 1,
              borderColor: tokens.border,
            }}
          >
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
            title="Scan QR code"
            tone="primary"
            disabled={busy}
            onPress={() => {
              void (async () => {
                const result = permission?.granted ? permission : await requestPermission();
                if (result.granted) {
                  scanLocked.current = false;
                  setScanning(true);
                } else
                  setError("Camera access is unavailable. Paste the pairing link below instead.");
              })().catch(() =>
                setError("Could not open the camera. Paste the pairing link instead."),
              );
            }}
          />
        )}
        {scanning && <Button title="Stop scanning" onPress={() => setScanning(false)} />}
        <Text style={[styles.eyebrow, { paddingTop: 12 }]}>Or paste a pairing link</Text>
        <TextInput
          accessibilityLabel="Pairing link"
          value={link}
          onChangeText={setLink}
          autoCorrect={false}
          autoCapitalize="none"
          secureTextEntry
          style={styles.input}
          placeholderTextColor={tokens.muted}
          placeholder="https://your-relay/#p=…"
          editable={!busy}
          maxLength={16_384}
        />
        {preview && (
          <View style={styles.card}>
            <Text style={styles.eyebrow}>Ready to pair</Text>
            <Text style={styles.heading}>{preview.name}</Text>
            <Text style={styles.secondary}>Relay · {preview.relay}</Text>
            <Text style={styles.secondary}>
              Confirm this is the computer whose terminal you scanned.
            </Text>
            {preview.localHTTP && (
              <Text style={styles.secondary}>
                Local network test: use trusted Wi-Fi. Messages are end-to-end encrypted; the relay
                connection uses HTTP.
              </Text>
            )}
          </View>
        )}
        {previewError && <Notice error>{previewError}</Notice>}
        <Button
          title={preview ? `Pair with ${preview.name}` : "Connect securely"}
          tone="primary"
          busy={busy}
          disabled={!preview || !name.trim()}
          onPress={() => void pair(link)}
        />
        {(error || externalError) && <Notice error>{error || externalError}</Notice>}
        <Text style={styles.secondary}>
          Treat this code like a password. It authorizes this phone to reach your computer.
        </Text>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
