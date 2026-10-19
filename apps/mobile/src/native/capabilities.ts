// The relay currently supports browser Web Push (RFC 8291), not FCM/APNs
// registration tokens. Do not send Expo push tokens to its endpoint route.
export const nativePushCapability = {
  supported: false,
  reason:
    "Native remote notifications require an FCM/APNs backend and signing credentials. In-app approvals work while connected.",
} as const;
