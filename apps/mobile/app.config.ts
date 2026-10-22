import type { ConfigContext, ExpoConfig } from "expo/config";

// Configure associations only after the real relay/RP domain is provisioned.
// The EAS project is linked; a hosted relay domain is configured separately.
const passkeyDomain = process.env.DECK_MOBILE_PASSKEY_DOMAIN;
if (passkeyDomain && !/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/i.test(passkeyDomain)) {
  throw new Error("DECK_MOBILE_PASSKEY_DOMAIN must be a hostname without scheme, port or path.");
}

export default ({ config }: ConfigContext): ExpoConfig => ({
  ...config,
  name: "Deck",
  slug: "deck-mobile",
  owner: "erioluwa-dev",
  extra: {
    ...config.extra,
    eas: {
      ...config.extra?.eas,
      projectId: "6da6d2a1-0650-421e-a017-f4ef3b57fed0",
    },
  },
  version: "0.1.1",
  scheme: "deck-mobile",
  orientation: "default",
  userInterfaceStyle: "automatic",
  icon: "./assets/icon.png",
  platforms: ["android", "ios"],
  ios: {
    supportsTablet: true,
    bundleIdentifier: "com.deckcli.mobile",
    ...(passkeyDomain
      ? {
          associatedDomains: [`webcredentials:${passkeyDomain}`, `applinks:${passkeyDomain}`],
        }
      : {}),
    infoPlist: { ITSAppUsesNonExemptEncryption: true },
  },
  android: {
    package: "com.deckcli.mobile",
    versionCode: 2,
    adaptiveIcon: {
      foregroundImage: "./assets/adaptive-icon.png",
      backgroundColor: "#F7F7F7",
    },
    ...(passkeyDomain
      ? {
          intentFilters: [
            {
              action: "VIEW",
              autoVerify: true,
              data: [{ scheme: "https", host: passkeyDomain }],
              category: ["BROWSABLE", "DEFAULT"],
            },
          ],
        }
      : {}),
  },
  plugins: [
    "./plugins/with-lan-http.cjs",
    [
      "expo-camera",
      {
        cameraPermission: "Allow Deck to scan a computer pairing QR code.",
        recordAudioAndroid: false,
        barcodeScannerEnabled: true,
      },
    ],
    "expo-secure-store",
    "expo-sqlite",
    "expo-dev-client",
  ],
});
