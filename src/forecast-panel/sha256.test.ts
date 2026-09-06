import { webcrypto } from 'crypto';
import { cacheKey } from './cacheKey';
import { sha256Bytes, sha256Hex } from './sha256';

const encode = (s: string) => new TextEncoder().encode(s);
const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');

describe('sha256Bytes (pure JS)', () => {
  it.each([
    ['', 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'],
    ['abc', 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'],
    [
      'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq',
      '248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1',
    ],
  ])('matches the FIPS vector for %j', (input, want) => {
    expect(hex(sha256Bytes(encode(input)))).toBe(want);
  });

  it('matches WebCrypto across the padding boundaries (0..130 bytes)', async () => {
    for (let n = 0; n <= 130; n++) {
      const input = encode('x'.repeat(n));
      const buf = await webcrypto.subtle.digest('SHA-256', input);
      expect(hex(sha256Bytes(input))).toBe(hex(new Uint8Array(buf)));
    }
  });

  it('matches WebCrypto on a long JSON payload', async () => {
    const json = JSON.stringify({ targets: Array.from({ length: 200 }, (_, i) => ({ expr: `up{i="${i}"}` })) });
    const buf = await webcrypto.subtle.digest('SHA-256', encode(json));
    expect(hex(sha256Bytes(encode(json)))).toBe(hex(new Uint8Array(buf)));
  });
});

describe('sha256Hex without crypto.subtle', () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'crypto');

  afterEach(() => {
    if (original) {
      Object.defineProperty(globalThis, 'crypto', original);
    } else {
      delete (globalThis as { crypto?: unknown }).crypto;
    }
  });

  it('falls back on insecure origins and yields the same digest', async () => {
    Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });
    const secure = await sha256Hex('hello');
    Object.defineProperty(globalThis, 'crypto', { value: { subtle: undefined }, configurable: true });
    const insecure = await sha256Hex('hello');
    expect(insecure).toBe(secure);
    expect(insecure).toBe('2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824');
  });

  it('cacheKey still returns 64 hex chars without crypto.subtle', async () => {
    Object.defineProperty(globalThis, 'crypto', { value: {}, configurable: true });
    const key = await cacheKey({
      targets: [{ refId: 'A', datasource: { uid: 'prom' }, expr: 'up' }],
      options: {
        model: 'holt',
        alpha: 0.8,
        beta: 0.2,
        period: 7,
        season: 'hour',
        calendar: '',
        showInterval: true,
        interval: 0.95,
        trainRange: { from: '', to: '' },
        forecastRange: { from: '', to: '' },
      },
      seriesName: 'up',
    });
    expect(key).toMatch(/^[a-f0-9]{64}$/);
  });
});
