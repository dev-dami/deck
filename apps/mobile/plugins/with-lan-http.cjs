const { withAndroidManifest } = require("expo/config-plugins");

// Only the explicit LAN profile permits Android HTTP/WS transport. Pairing
// validation still refuses public HTTP origins; Noise remains end-to-end.
module.exports = (config) =>
  withAndroidManifest(config, (mod) => {
    const application = mod.modResults.manifest.application?.[0];
    if (!application) throw new Error("Android application manifest is missing");
    application.$["android:usesCleartextTraffic"] =
      process.env.EXPO_PUBLIC_DECK_LAN_HTTP === "1" ? "true" : "false";
    return mod;
  });
