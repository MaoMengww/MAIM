// Persist only an opaque key and a request hash, never source evidence or code.
// A timeout must retry the same intent, including after closing the panel.
const fallbackKeys = new Map<string, string>();
const inFlight = new Map<string, Promise<unknown>>();
async function intentStorageKey(scope: string, payload: unknown): Promise<string> {
  const bytes = new TextEncoder().encode(JSON.stringify(payload));
  const hash = await crypto.subtle.digest('SHA-256', bytes);
  const fingerprint = Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('');
  return `aim-skill-intent:${scope}:${fingerprint}`;
}

function submissionKeyAt(storageKey: string): string {
  try {
    const existing = sessionStorage.getItem(storageKey);
    if (existing) return existing;
    const key = fallbackKeys.get(storageKey) ?? crypto.randomUUID();
    sessionStorage.setItem(storageKey, key);
    fallbackKeys.set(storageKey, key);
    return key;
  } catch {
    const key = fallbackKeys.get(storageKey) ?? crypto.randomUUID();
    fallbackKeys.set(storageKey, key);
    return key;
  }
}

export async function skillSubmissionKey(scope: string, payload: unknown): Promise<string> {
  return submissionKeyAt(await intentStorageKey(scope, payload));
}

// A known-success response completes an intent; a later explicit action,
// including renewed approval after reauthorization, receives a fresh key.
// Failures and unknown outcomes preserve the old key, and concurrent callers
// in this client share one pending request rather than executing twice.
export async function submitSkillIntent<T>(scope: string, payload: unknown, execute: (key: string) => Promise<T>): Promise<T> {
  const pendingKey = JSON.stringify([scope, payload]);
  const existing = inFlight.get(pendingKey);
  if (existing) return existing as Promise<T>;
  const job = Promise.resolve().then(async () => {
    const storageKey = await intentStorageKey(scope, payload);
    const key = submissionKeyAt(storageKey);
    const result = await execute(key);
    try {
      if (sessionStorage.getItem(storageKey) === key) sessionStorage.removeItem(storageKey);
    } catch { /* Storage can be unavailable; preserve the in-memory lifecycle. */ }
    if (fallbackKeys.get(storageKey) === key) fallbackKeys.delete(storageKey);
    return result;
  });
  inFlight.set(pendingKey, job);
  try { return await job; }
  finally { if (inFlight.get(pendingKey) === job) inFlight.delete(pendingKey); }
}

export async function skillFileBase64(file: File): Promise<string> {
  if (file.size > 65536) throw new Error('验证样例不得超过 64 KiB');
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary);
}
