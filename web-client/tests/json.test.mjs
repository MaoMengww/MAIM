import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

let parser, directory;

before(async () => {
  directory = await mkdtemp(join(tmpdir(), 'aim-json-'));
  const outfile = join(directory, 'json.mjs');
  await build({ entryPoints: ['src/utils/json.ts'], bundle: true, platform: 'node', format: 'esm', outfile });
  parser = await import(pathToFileURL(outfile));
});
after(async () => { await rm(directory, { recursive: true, force: true }); });

test('preserves adjacent snowflake IDs without rewriting JSON strings or escapes', () => {
  const text = String.raw`{"ids":[2107179204238905344,2107179204238905345],"body":"2107179204238905345 and -2107179204238905344, 1.25, 2e19","escaped":"quote: \"2107179204238905345\", slash: \\, newline: \n","2107179204238905344":"string key"}`;
  const expected = {
    ids: ['2107179204238905344', '2107179204238905345'],
    body: '2107179204238905345 and -2107179204238905344, 1.25, 2e19',
    escaped: 'quote: "2107179204238905345", slash: \\, newline: \n',
    '2107179204238905344': 'string key',
  };
  assert.deepEqual(parser.safeJsonParse(text), expected);
  assert.deepEqual(parser.parseJsonWithExactIntegers(text), expected);
});

test('keeps safe integer boundaries numeric and preserves negative unsafe literals', () => {
  assert.deepEqual(parser.parseJsonWithExactIntegers('[0,-0,9007199254740991,-9007199254740991,9007199254740992,-9007199254740992,-2107179204238905345]'),
    [0, -0, 9007199254740991, -9007199254740991, '9007199254740992', '-9007199254740992', '-2107179204238905345']);
  assert.equal(parser.parseJsonWithExactIntegers(' \n2107179204238905345\t'), '2107179204238905345');
});

test('keeps fractions and exponent notation under native JSON number semantics', () => {
  const text = '[1.25,-0.5,2107179204238905345.0,2e19,-2E+19,2e-3,1e400]';
  assert.deepEqual(parser.parseJsonWithExactIntegers(text), JSON.parse(text));
});

test('rejects malformed JSON rather than repairing unsafe number tokens', () => {
  assert.throws(() => parser.parseJsonWithExactIntegers('{2107179204238905345:1}'), SyntaxError);
  assert.throws(() => parser.parseJsonWithExactIntegers('{2107179204238905345 \n:1}'), SyntaxError);
  assert.throws(() => parser.parseJsonWithExactIntegers('[02107179204238905345]'), SyntaxError);
  assert.throws(() => parser.parseJsonWithExactIntegers('[2107179204238905345,]'), SyntaxError);
  assert.throws(() => parser.parseJsonWithExactIntegers('[2107179204238905345e+]'), SyntaxError);
  const invalidEscape = String.raw`{"id":2107179204238905345,"text":"\q"}`;
  assert.throws(() => parser.parseJsonWithExactIntegers(invalidEscape), SyntaxError);
  assert.equal(parser.safeJsonParse(invalidEscape), invalidEscape);
});
