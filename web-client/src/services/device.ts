const DEVICE_STORAGE_KEY = 'aim_device_id';
const DEVICE_PLATFORM = 'web';
// Device identity survives reloads and logout; each connection still has its own incarnation.
let deviceId: string | null = null;

export function getDeviceId(): string {
  if (deviceId) return deviceId;
  const stored = localStorage.getItem(DEVICE_STORAGE_KEY);
  if (stored) {
    deviceId = stored;
    return deviceId;
  }
  const created = crypto.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
  localStorage.setItem(DEVICE_STORAGE_KEY, created);
  deviceId = created;
  return deviceId;
}

// The authenticated identity carried on login/register must match the identity
// the WebSocket registers with, otherwise device-scoped state forks per call.
export function deviceCredentials(): { device_id: string; platform: string } {
  return { device_id: getDeviceId(), platform: DEVICE_PLATFORM };
}
