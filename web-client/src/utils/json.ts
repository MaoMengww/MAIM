const MAX_SAFE_INTEGER = '9007199254740991';

// Quote unsafe integer tokens before JSON.parse can round snowflake IDs.
// JSON.parse still validates strings, escapes and the surrounding JSON structure.
export function parseJsonWithExactIntegers(text: string): unknown {
  let index = 0;
  let copiedUntil = 0;
  let parts: string[] | undefined;

  while (index < text.length) {
    const char = text[index];
    if (char === '"') {
      index++;
      while (index < text.length) {
        if (text[index] === '\\') {
          index += 2;
        } else if (text[index++] === '"') {
          break;
        }
      }
      continue;
    }
    if (char !== '-' && (char < '0' || char > '9')) {
      index++;
      continue;
    }

    const start = index;
    if (char === '-') index++;
    const digitsStart = index;
    if (text[index] === '0') {
      index++;
    } else {
      if (!(text[index] >= '1' && text[index] <= '9')) throw new SyntaxError('Invalid JSON number');
      while (text[index] >= '0' && text[index] <= '9') index++;
    }
    const digitsEnd = index;
    let integer = true;
    if (text[index] === '.') {
      integer = false;
      index++;
      if (!(text[index] >= '0' && text[index] <= '9')) throw new SyntaxError('Invalid JSON number');
      while (text[index] >= '0' && text[index] <= '9') index++;
    }
    if (text[index] === 'e' || text[index] === 'E') {
      integer = false;
      index++;
      if (text[index] === '+' || text[index] === '-') index++;
      if (!(text[index] >= '0' && text[index] <= '9')) throw new SyntaxError('Invalid JSON number');
      while (text[index] >= '0' && text[index] <= '9') index++;
    }
    // Reject leading zeros and bare numeric keys instead of making invalid JSON valid.
    const end = index;
    while (index < text.length && ' \t\r\n'.includes(text[index])) index++;
    if (index < text.length && !',]}'.includes(text[index])) throw new SyntaxError('Invalid JSON number');

    const digitsLength = digitsEnd - digitsStart;
    if (integer && (digitsLength > MAX_SAFE_INTEGER.length ||
      (digitsLength === MAX_SAFE_INTEGER.length && text.slice(digitsStart, digitsEnd) > MAX_SAFE_INTEGER))) {
      parts ??= [];
      parts.push(text.slice(copiedUntil, start), `"${text.slice(start, end)}"`);
      copiedUntil = end;
    }
  }

  if (!parts) return JSON.parse(text);
  parts.push(text.slice(copiedUntil));
  return JSON.parse(parts.join(''));
}

export function safeJsonParse<T = any>(text: string): T {
  if (typeof text !== 'string') return text as T;
  try {
    return parseJsonWithExactIntegers(text) as T;
  } catch {
    return text as T;
  }
}
