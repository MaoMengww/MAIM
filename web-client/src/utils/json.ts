// Use JSON.parse reviver to convert large integers to strings during parse,
// preserving precision for snowflake IDs (exceeding Number.MAX_SAFE_INTEGER = 9e15).
export function safeJsonParse<T = any>(text: string): T {
  if (typeof text !== 'string') return text as T;
  try {
    return JSON.parse(text, (_key, value) => {
      if (typeof value === 'number' && !Number.isSafeInteger(value)) {
        return String(value);
      }
      return value;
    });
  } catch {
    return text as T;
  }
}
