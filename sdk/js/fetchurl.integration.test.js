import { describe, it } from 'node:test';
import { Buffer } from 'node:buffer';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { GenericContainer, Network, Wait } from 'testcontainers';
import { fetchurl, hashData } from './fetchurl.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

describe('fetchurl integration (testcontainers)', { timeout: 120_000 }, () => {
  it('fetches through fetchurl server from a source URL', async () => {
    const content = Buffer.from('integration-test');
    const hash = await hashData('sha256', content);

    const repoRoot = path.resolve(__dirname, '..', '..');
    const network = await new Network().start();

    const imageRef = process.env.FETCHURL_TEST_IMAGE;
    let container;
    const oldEnv = process.env.FETCHURL_SERVER;
    let upstream;
    try {
      upstream = await new GenericContainer('python:3.12-alpine')
        .withNetwork(network)
        .withNetworkAliases('upstream')
        .withCopyContentToContainer([
          { content: content.toString('utf8'), target: '/srv/file' },
        ])
        .withExposedPorts(8000)
        .withCommand([
          'python',
          '-m',
          'http.server',
          '8000',
          '--bind',
          '0.0.0.0',
          '--directory',
          '/srv',
        ])
        .withWaitStrategy(Wait.forHttp('/file', 8000))
        .start();

      if (imageRef) {
        container = await new GenericContainer(imageRef)
          .withCommand(['server'])
          .withNetwork(network)
          .withExposedPorts(8080)
          .withEnvironment({ FETCHURL_ALLOW_PRIVATE_IPS: '1' })
          .withWaitStrategy(Wait.forLogMessage(/Starting server/i))
          .start();
      } else {
        const image = await GenericContainer.fromDockerfile(repoRoot).build();
        container = await image
          .withCommand(['server'])
          .withNetwork(network)
          .withExposedPorts(8080)
          .withEnvironment({ FETCHURL_ALLOW_PRIVATE_IPS: '1' })
          .withWaitStrategy(Wait.forLogMessage(/Starting server/i))
          .start();
      }

      const host = container.getHost();
      const port = container.getMappedPort(8080);
      process.env.FETCHURL_SERVER = `"http://${host}:${port}/api/fetchurl"`;

      const netName = network.getName();
      const upstreamIp = upstream.getIpAddress(netName);
      const sourceUrl = `http://${upstreamIp}:8000/file`;
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
      if (upstream) await upstream.stop();
      await network.stop();
    }
  });
});
