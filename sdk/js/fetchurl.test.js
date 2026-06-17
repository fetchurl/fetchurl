import { describe, it } from 'node:test';
import { createServer } from 'node:http';
import assert from 'node:assert/strict';
import {
  normalizeAlgo,
  isSupported,
  encodeSourceUrls,
  parseFetchurlServer,
  hashData,
  verifyHash,
  FetchSession,
  fetchurl,
  UnsupportedAlgorithmError,
  MissingSourceUrlsError,
  HashMismatchError,
  AllSourcesFailedError,
  PartialWriteError,
} from './fetchurl.js';

/** Start a test HTTP server, returns { url, close }. */
function startServer(handler) {
  return new Promise((resolve) => {
    const server = createServer(handler);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}`,
        close: () => server.close(),
      });
    });
  });
}

async function sha256hex(data) {
  return hashData('sha256', data);
}

// Helper to run with env var
async function withEnv(val, fn) {
  const old = process.env.FETCHURL_SERVER;
  process.env.FETCHURL_SERVER = val;
  try {
    await fn();
  } finally {
    if (old === undefined) delete process.env.FETCHURL_SERVER;
    else process.env.FETCHURL_SERVER = old;
  }
}

// --- Unit tests ---

describe('normalizeAlgo', () => {
  it('lowercases and strips non-alnum', () => {
    assert.equal(normalizeAlgo('SHA-256'), 'sha256');
    assert.equal(normalizeAlgo('sha256'), 'sha256');
    assert.equal(normalizeAlgo('SHA_512'), 'sha512');
  });
});

describe('isSupported', () => {
  it('accepts supported algos', () => {
    assert.ok(isSupported('sha256'));
    assert.ok(isSupported('SHA-256'));
    assert.ok(isSupported('sha1'));
    assert.ok(isSupported('sha512'));
  });

  it('rejects unsupported algos', () => {
    assert.ok(!isSupported('md5'));
  });
});

describe('SFV', () => {
  it('encodes string list', () => {
    assert.equal(
      encodeSourceUrls(['https://a.com', 'https://b.com']),
      '"https://a.com", "https://b.com"',
    );
  });

  it('parses string list', () => {
    assert.deepEqual(
      parseFetchurlServer('"https://a.com", "https://b.com"'),
      ['https://a.com', 'https://b.com'],
    );
  });

  it('roundtrips', () => {
    const urls = ['https://cdn.example.com/f.tar.gz', 'https://mirror.org/a.tgz'];
    assert.deepEqual(parseFetchurlServer(encodeSourceUrls(urls)), urls);
  });

  it('handles parameters', () => {
    assert.deepEqual(
      parseFetchurlServer('"https://a.com";q=0.9, "https://b.com"'),
      ['https://a.com', 'https://b.com'],
    );
  });

  it('handles empty', () => {
    assert.deepEqual(parseFetchurlServer(''), []);
  });
});

describe('hashData / verifyHash', () => {
  it('hashes correctly', async () => {
    const data = new TextEncoder().encode('hello world');
    const hash = await hashData('sha256', data);
    assert.equal(
      hash,
      'b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9',
    );
  });

  it('verifyHash passes on match', async () => {
    const data = new TextEncoder().encode('hello world');
    const hash = await sha256hex(data);
    await verifyHash('sha256', hash, data);
  });

  it('verifyHash throws on mismatch', async () => {
    const data = new TextEncoder().encode('hello world');
    await assert.rejects(
      () => verifyHash('sha256', 'badhash', data),
      HashMismatchError,
    );
  });
});

describe('FetchSession', () => {
  it('rejects missing sourceUrls', () => {
    assert.throws(
      () => new FetchSession({ algo: 'sha256', hash: 'abc', sourceUrls: [] }),
      MissingSourceUrlsError,
    );
  });

  it('rejects unsupported algo', () => {
    assert.throws(
      () => new FetchSession({ algo: 'md5', hash: 'abc', sourceUrls: ['http://src'] }),
      UnsupportedAlgorithmError,
    );
  });

  it('orders servers before sources', async () => {
    const hash = await sha256hex(new TextEncoder().encode('test'));
    await withEnv('"http://cache1/api/fetchurl", "http://cache2/api/fetchurl"', async () => {
      const session = new FetchSession({
        algo: 'sha256',
        hash,
        sourceUrls: ['http://src1'],
      });

      const a1 = session.nextAttempt();
      assert.ok(a1.url.startsWith('http://cache1/api/fetchurl/sha256/'));
      assert.ok(a1.headers['X-Source-Urls']);

      const a2 = session.nextAttempt();
      assert.ok(a2.url.startsWith('http://cache2/api/fetchurl/sha256/'));

      const a3 = session.nextAttempt();
      assert.equal(a3.url, 'http://src1');
      assert.deepEqual(a3.headers, {});

      assert.equal(session.nextAttempt(), null);
      assert.ok(!session.succeeded());
    });
  });

  it('stops after reportSuccess', async () => {
    const hash = await sha256hex(new TextEncoder().encode('test'));
    await withEnv('"http://cache/api/fetchurl"', async () => {
      const session = new FetchSession({
        algo: 'sha256',
        hash,
        sourceUrls: ['http://src'],
      });
      session.nextAttempt();
      session.reportSuccess();
      assert.ok(session.succeeded());
      assert.equal(session.nextAttempt(), null);
    });
  });

  it('stops after reportPartial', async () => {
    const hash = await sha256hex(new TextEncoder().encode('test'));
    await withEnv('"http://cache/api/fetchurl"', async () => {
      const session = new FetchSession({
        algo: 'sha256',
        hash,
        sourceUrls: ['http://src'],
      });
      session.nextAttempt();
      session.reportPartial();
      assert.ok(!session.succeeded());
      assert.equal(session.nextAttempt(), null);
    });
  });
});

// --- Integration tests ---

describe('fetchurl()', () => {
  it('fetches and verifies from direct source', async () => {
    const content = new TextEncoder().encode('test content');
    const hash = await sha256hex(content);

    const srv = await startServer((_req, res) => {
      res.writeHead(200);
      res.end(content);
    });
    try {
      // Ensure no servers configured
      await withEnv('', async () => {
        const data = await fetchurl({
          fetch,
          algo: 'sha256',
          hash,
          sourceUrls: [srv.url],
        });
        assert.deepEqual(data, content);
      });
    } finally {
      srv.close();
    }
  });

  it('throws PartialWriteError on hash mismatch', async () => {
    const content = new TextEncoder().encode('wrong content');
    const hash = await sha256hex(new TextEncoder().encode('right content'));

    const srv = await startServer((_req, res) => {
      res.writeHead(200);
      res.end(content);
    });
    try {
      await withEnv('', async () => {
        await assert.rejects(
          () =>
            fetchurl({
              fetch,
              algo: 'sha256',
              hash,
              sourceUrls: [srv.url],
            }),
          PartialWriteError,
        );
      });
    } finally {
      srv.close();
    }
  });

  it('throws AllSourcesFailedError when all fail', async () => {
    const hash = await sha256hex(new TextEncoder().encode('x'));

    const srv = await startServer((_req, res) => {
      res.writeHead(404);
      res.end();
    });
    try {
      await withEnv('', async () => {
        await assert.rejects(
          () =>
            fetchurl({
              fetch,
              algo: 'sha256',
              hash,
              sourceUrls: [srv.url],
            }),
          AllSourcesFailedError,
        );
      });
    } finally {
      srv.close();
    }
  });

  it('falls back from failed server to direct source', async () => {
    const content = new TextEncoder().encode('fallback content');
    const hash = await sha256hex(content);

    const bad = await startServer((_req, res) => {
      res.writeHead(500);
      res.end();
    });
    const good = await startServer((_req, res) => {
      res.writeHead(200);
      res.end(content);
    });
    try {
      await withEnv(`"${bad.url}/api/fetchurl"`, async () => {
        const data = await fetchurl({
          fetch,
          algo: 'sha256',
          hash,
          sourceUrls: [good.url],
        });
        assert.deepEqual(data, content);
      });
    } finally {
      bad.close();
      good.close();
    }
  });
});
