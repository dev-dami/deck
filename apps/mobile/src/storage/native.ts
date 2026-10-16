import { NativeStoreBackend, type SecretVault } from "./backend";

export async function openNativeStorage(): Promise<NativeStoreBackend> {
  const [SQLite, SecureStore] = await Promise.all([
    import("expo-sqlite"),
    import("expo-secure-store"),
  ]);
  if (!(await SecureStore.isAvailableAsync())) {
    throw new Error("Secure device storage is unavailable. Pairing has been disabled.");
  }
  const options = {
    keychainService: "deck.mobile.identity.v1",
    keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
  };
  const vault: SecretVault = {
    async get(key) {
      try {
        return await SecureStore.getItemAsync(key, options);
      } catch {
        throw new Error("Secure identity access failed. Unlock your phone and retry.");
      }
    },
    async set(key, value) {
      try {
        await SecureStore.setItemAsync(key, value, options);
      } catch {
        throw new Error("Secure identity could not be saved. Unlock your phone and retry.");
      }
    },
    async remove(key) {
      try {
        await SecureStore.deleteItemAsync(key, options);
      } catch {
        throw new Error("Secure identity could not be removed. Unlock your phone and retry.");
      }
    },
  };
  const backend = new NativeStoreBackend(
    await SQLite.openDatabaseAsync("deck-mobile-v3.db"),
    vault,
  );
  await backend.initialize();
  return backend;
}
