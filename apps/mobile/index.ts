import { registerRootComponent } from "expo";
import { install, CryptoKey as NativeCryptoKey } from "react-native-quick-crypto";

// Shared Noise modules read global crypto at runtime. Requiring App only
// AFTER installation prevents static import evaluation from racing bootstrap.
install();
Object.defineProperty(globalThis, "CryptoKey", {
  value: NativeCryptoKey,
  configurable: true,
  writable: true,
});
const App = require("./App").default;
registerRootComponent(App);
