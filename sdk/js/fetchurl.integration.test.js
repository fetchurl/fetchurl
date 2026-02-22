import { describe, it } from 'node:test';
import { createServer } from 'node:http';
import { Buffer } from 'node:buffer';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { GenericContainer, Wait } from 'testcontainers';
import { fetchurl, hashData } from './fetchurl.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function startUpstreamServer(content) {
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      if (req.url !== '/file') {
        res.writeHead(404);
        res.end();
        return;
      }
      res.setHeader('Content-Type', 'application/octet-stream');
      res.setHeader('Content-Length', String(content.length));
      res.writeHead(200);
      res.end(content);
    });
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}`,
        close: () => server.close(),
        port,
      });
    });
  });
}

describe('fetchurl integration (testcontainers)', { timeout: 120_000 }, () => {
  it('fetches through fetchurl server from a source URL', async () => {
    const content = Buffer.from('integration-test');
    const hash = await hashData('sha256', content);

    const upstream = await startUpstreamServer(content);
    const repoRoot = path.resolve(__dirname, '..', '..');

    const imageRef = process.env.FETCHURL_TEST_IMAGE;
    let container;
    const oldEnv = process.env.FETCHURL_SERVER;
    try {
      if (imageRef) {
        container = await new GenericContainer(imageRef)
          .withCommand(['server'])
          .withExposedPorts(8080)
          .withWaitStrategy(Wait.forLogMessage(/Starting server/i))
          .start();
      } else {
        const image = await GenericContainer.fromDockerfile(repoRoot).build();
        container = await image
          .withCommand(['server'])
          .withExposedPorts(8080)
          .withWaitStrategy(Wait.forLogMessage(/Starting server/i))
          .start();
      }

      const host = container.getHost();
      const port = container.getMappedPort(8080);
      process.env.FETCHURL_SERVER = `"http://${host}:${port}"`;

      const sourceUrl = `http://host.testcontainers.internal:${upstream.port}/file`;
      const data = await fetchurl({
        fetch,
        algo: 'sha256',
        hash,
        sourceUrls: [sourceUrl],
      });

      assert.deepEqual(data, new Uint8Array(content));
    } finally {
      process.env.FETCHURL_SERVER = oldEnv;
      if (container) await container.stop();
      upstream.close();
    }
  });
});
