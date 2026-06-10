/**
 * Fetchurl SDK for JavaScript.
 *
 * Protocol-level client for fetchurl content-addressable cache servers.
 * Uses Web Crypto API — works in Node.js 19+, Deno, Bun, and browsers.
 * Pass any spec-compliant `fetch` function for dependency injection.
 *
 * @example
 * import { fetchurl, parseFetchurlServer } from './fetchurl.js';
 *
 * const servers = parseFetchurlServer(process.env.FETCHURL_SERVER ?? '');
 * const data = await fetchurl({
 *   fetch,
 *   servers,
 *   algo: 'sha256',
 *   hash: 'e3b0c44...',
 *   sourceUrls: ['https://cdn.example.com/file.tar.gz'],
 * });
 * // data is Uint8Array, hash-verified
 *
 * @module fetchurl
 */

// --- Errors ---

export class FetchUrlError extends Error {
  constructor(message) {
    super(message);
    this.name = 'FetchUrlError';
  }
}

export class UnsupportedAlgorithmError extends FetchUrlError {
  constructor(algo) {
    super(`unsupported algorithm: ${algo}`);
    this.name = 'UnsupportedAlgorithmError';
    this.algo = algo;
  }
}

export class HashMismatchError extends FetchUrlError {
  constructor(expected, actual) {
    super(`hash mismatch: expected ${expected}, got ${actual}`);
    this.name = 'HashMismatchError';
    this.expected = expected;
    this.actual = actual;
  }
}

export class AllSourcesFailedError extends FetchUrlError {
  constructor(lastError = null) {
    super('all sources failed');
    this.name = 'AllSourcesFailedError';
    this.lastError = lastError;
  }
}

export class PartialWriteError extends FetchUrlError {
  constructor(cause) {
    super(`partial write: ${cause?.message ?? cause}`);
    this.name = 'PartialWriteError';
    this.cause = cause;
  }
}

export class MissingSourceUrlsError extends FetchUrlError {
  constructor() {
    super('sourceUrls is required');
    this.name = 'MissingSourceUrlsError';
  }
}

// --- Algorithm helpers ---

/** Map from normalized algo name to Web Crypto algorithm identifier. */
const WEBCRYPTO_ALGOS = {
  sha1: 'SHA-1',
  sha256: 'SHA-256',
  sha512: 'SHA-512',
};

/**
 * Normalize algorithm name per spec: lowercase, only [a-z0-9].
 * @param {string} name
 * @returns {string}
 */
export function normalizeAlgo(name) {
  return name.toLowerCase().replace(/[^a-z0-9]/g, '');
}

/**
 * Check if a hash algorithm is supported.
 * @param {string} algo
 * @returns {boolean}
 */
export function isSupported(algo) {
  return normalizeAlgo(algo) in WEBCRYPTO_ALGOS;
}

// --- SFV helpers (RFC 8941 string lists) ---

/**
 * Encode URLs as an RFC 8941 string list for the X-Source-Urls header.
 * @param {string[]} urls
 * @returns {string}
 */
export function encodeSourceUrls(urls) {
  return urls
    .map((url) => `"${url.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`)
    .join(', ');
}

/**
 * Parse FETCHURL_SERVER env var (RFC 8941 string list).
 * @param {string} value
 * @returns {string[]}
 */
export function parseFetchurlServer(value) {
  value = value.trim();
  if (value === '') return [];
  if (!value.startsWith('"')) {
    return [value];
  }
  const results = [];
  let i = 0;
  while (i < value.length) {
    while (i < value.length && (value[i] === ' ' || value[i] === '\t')) i++;
    if (i >= value.length) break;

    if (value[i] !== '"') {
      while (i < value.length && value[i] !== ',') i++;
      if (i < value.length) i++;
      continue;
    }
    i++;

    let s = '';
    while (i < value.length) {
      if (value[i] === '\\' && i + 1 < value.length) {
        s += value[i + 1];
        i += 2;
      } else if (value[i] === '"') {
        i++;
        break;
      } else {
        s += value[i];
        i++;
      }
    }
    results.push(s);

    while (i < value.length && value[i] !== ',') i++;
    if (i < value.length) i++;
  }
  return results;
}

// --- Hashing ---

/**
 * Try to import node:crypto for incremental hashing (Node/Deno/Bun).
 * Falls back to Web Crypto (buffers entire content) in browsers.
 */
let _nodeCrypto = null;
try {
  _nodeCrypto = await import('node:crypto');
} catch {
  // Not available (browser) — will use Web Crypto fallback
}

function toHex(buffer) {
  return Array.from(new Uint8Array(buffer))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/**
 * Create an incremental hasher.
 *
 * Uses node:crypto when available (streaming, no buffering).
 * Falls back to Web Crypto (must call finish() with full data).
 *
 * @param {string} algo - Normalized algo name.
 * @returns {{ update(chunk: Uint8Array): void, finish(): Promise<string> }}
 */
export function createHasher(algo) {
  if (_nodeCrypto) {
    const h = _nodeCrypto.createHash(algo);
    return {
      update(chunk) {
        h.update(chunk);
      },
      async finish() {
        return h.digest('hex');
      },
    };
  }
  // Web Crypto fallback — accumulate and hash at the end
  const chunks = [];
  let totalLen = 0;
  return {
    update(chunk) {
      chunks.push(new Uint8Array(chunk));
      totalLen += chunk.byteLength;
    },
    async finish() {
      const full = new Uint8Array(totalLen);
      let offset = 0;
      for (const c of chunks) {
        full.set(c, offset);
        offset += c.byteLength;
      }
      const webAlgo = WEBCRYPTO_ALGOS[algo];
      return toHex(await crypto.subtle.digest(webAlgo, full));
    },
  };
}

/**
 * Hash data and return hex string.
 * @param {string} algo - Normalized algo name (sha1, sha256, sha512).
 * @param {Uint8Array} data
 * @returns {Promise<string>} Hex hash.
 */
export async function hashData(algo, data) {
  const h = createHasher(algo);
  h.update(data);
  return h.finish();
}

/**
 * Verify that data matches the expected hash.
 * @param {string} algo - Normalized algo name.
 * @param {string} expectedHash - Expected hex hash.
 * @param {Uint8Array} data
 * @returns {Promise<void>}
 * @throws {HashMismatchError}
 */
export async function verifyHash(algo, expectedHash, data) {
  const actual = await hashData(algo, data);
  if (actual !== expectedHash) {
    throw new HashMismatchError(expectedHash, actual);
  }
}

// --- FetchAttempt ---

/**
 * @typedef {Object} FetchAttempt
 * @property {string} url - The URL to GET.
 * @property {Record<string, string>} headers - Headers to include.
 */

// --- FetchSession ---

/**
 * State machine driving the fetchurl client protocol.
 *
 * Servers are tried first (with X-Source-Urls), then direct
 * source URLs in random order per spec.
 */
export class FetchSession {
  #attempts = [];
  #current = 0;
  #algo;
  #hash;
  #done = false;
  #success = false;

  /**
   * @param {Object} options
   * @param {string[]} options.servers - Cache server base URLs.
   * @param {string} options.algo - Hash algorithm name.
   * @param {string} options.hash - Expected hex hash.
   * @param {string[]} options.sourceUrls - Direct source URLs.
   */
  constructor({ servers, algo, hash, sourceUrls = [] }) {
    if (!servers || servers.length === 0) {
      if (typeof process !== 'undefined' && process.env) {
        servers = parseFetchurlServer(process.env.FETCHURL_SERVER || '');
      } else {
        servers = [];
      }
    }

    if (!Array.isArray(sourceUrls) || sourceUrls.length === 0) {
      throw new MissingSourceUrlsError();
    }

    this.#algo = normalizeAlgo(algo);
    if (!isSupported(this.#algo)) {
      throw new UnsupportedAlgorithmError(this.#algo);
    }
    this.#hash = hash;

    const sourceHeader =
      sourceUrls.length > 0 ? encodeSourceUrls(sourceUrls) : null;

    for (const server of servers) {
      const base = server.replace(/\/+$/, '');
      const url = `${base}/${this.#algo}/${hash}`;
      const headers = {};
      if (sourceHeader) headers['X-Source-Urls'] = sourceHeader;
      this.#attempts.push({ url, headers });
    }

    const shuffled = [...sourceUrls];
    for (let i = shuffled.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
    }
    for (const url of shuffled) {
      this.#attempts.push({ url, headers: {} });
    }
  }

  /** Algorithm used (normalized). */
  get algo() {
    return this.#algo;
  }

  /** Expected hash. */
  get hash() {
    return this.#hash;
  }

  /**
   * Get the next attempt, or null if session is finished.
   * If an attempt fails without writing bytes, just call nextAttempt() again.
   * @returns {FetchAttempt | null}
   */
  nextAttempt() {
    if (this.#done || this.#current >= this.#attempts.length) return null;
    return this.#attempts[this.#current++];
  }

  /** Mark session as successful. */
  reportSuccess() {
    this.#done = true;
    this.#success = true;
  }

  /** Mark that bytes were written before failure. Stops further attempts. */
  reportPartial() {
    this.#done = true;
  }

  /** @returns {boolean} */
  succeeded() {
    return this.#success;
  }
}

// --- High-level fetch ---

/**
 * Fetch and verify a file from fetchurl servers or direct sources.
 *
 * @param {Object} options
 * @param {typeof globalThis.fetch} options.fetch - The fetch function (DI).
 * @param {string[]} [options.servers] - Cache server base URLs.
 * @param {string} options.algo - Hash algorithm (sha1, sha256, sha512).
 * @param {string} options.hash - Expected hex hash.
 * @param {string[]} [options.sourceUrls] - Direct source URLs.
 * @returns {Promise<Uint8Array>} Hash-verified content.
 * @throws {AllSourcesFailedError|PartialWriteError|UnsupportedAlgorithmError}
 */
export async function fetchurl({
  fetch: fetchFn,
  servers = [],
  algo,
  hash,
  sourceUrls = [],
}) {
  const session = new FetchSession({ servers, algo, hash, sourceUrls });
  let lastError = null;
  let attempt = session.nextAttempt();

  while (attempt) {
    let resp;
    try {
      resp = await fetchFn(attempt.url, { headers: attempt.headers });
    } catch (e) {
      lastError = e;
      attempt = session.nextAttempt();
      continue;
    }

    if (!resp.ok) {
      lastError = new FetchUrlError(`unexpected status ${resp.status}`);
      attempt = session.nextAttempt();
      continue;
    }

    const hasher = createHasher(session.algo);
    const chunks = [];
    let bytesRead = 0;
    try {
      for await (const chunk of resp.body) {
        hasher.update(chunk);
        chunks.push(new Uint8Array(chunk));
        bytesRead += chunk.byteLength;
      }
      const actualHash = await hasher.finish();
      if (actualHash !== session.hash) {
        throw new HashMismatchError(session.hash, actualHash);
      }
      const result = new Uint8Array(bytesRead);
      let offset = 0;
      for (const c of chunks) {
        result.set(c, offset);
        offset += c.byteLength;
      }
      session.reportSuccess();
      return result;
    } catch (e) {
      lastError = e;
      if (bytesRead > 0) {
        session.reportPartial();
        throw new PartialWriteError(e);
      }
    }
  }

  throw new AllSourcesFailedError(lastError);
}
