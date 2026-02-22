"""Integration test for Python SDK using Testcontainers."""

import hashlib
import os
import tempfile
import unittest
import signal
import time
from pathlib import Path

import fetchurl
from testcontainers.core.container import DockerContainer
from testcontainers.core.network import Network


def sha256hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class TestIntegration(unittest.TestCase):
    def test_fetchurl_server_integration(self):
        old_handler = None
        if hasattr(signal, "SIGALRM"):
            def _timeout_handler(signum, frame):
                raise TimeoutError("integration test timed out")

            old_handler = signal.signal(signal.SIGALRM, _timeout_handler)
            signal.alarm(60)
        content = b"integration-test"
        hash_hex = sha256hex(content)

        repo_root = Path(__file__).resolve().parents[2]
        old_env = os.environ.get("FETCHURL_SERVER")

        with tempfile.TemporaryDirectory() as tmpdir:
            data_dir = Path(tmpdir)
            (data_dir / "file").write_bytes(content)

            net = Network().create()
            server = None
            upstream = None
            try:
                upstream = (
                    DockerContainer("python:3.12-alpine")
                    .with_network(net)
                    .with_network_aliases("upstream")
                    .with_volume_mapping(str(data_dir), "/srv", mode="ro")
                    .with_command(
                        "python -m http.server 8000 --bind 0.0.0.0 --directory /srv"
                    )
                ).start()

                image_ref = os.environ.get("FETCHURL_TEST_IMAGE")
                if not image_ref:
                    self.fail("FETCHURL_TEST_IMAGE is required for integration test")
                server = (
                    DockerContainer(image_ref)
                    .with_command("server")
                    .with_network(net)
                    .with_exposed_ports(8080)
                ).start()

                time.sleep(1)

                class TimeoutFetcher:
                    def get(self, url, headers):
                        import urllib.request

                        req = urllib.request.Request(url, headers=headers)
                        resp = urllib.request.urlopen(req, timeout=10)
                        return (resp.status, resp)

                out = tempfile.TemporaryFile()
                try:
                    host = server.get_container_host_ip()
                    port = server.get_exposed_port(8080)
                    os.environ["FETCHURL_SERVER"] = f"\"http://{host}:{port}\""

                    fetchurl.fetch(
                        TimeoutFetcher(),
                        "sha256",
                        hash_hex,
                        ["http://upstream:8000/file"],
                        out=out,
                    )
                    out.seek(0)
                    fetched = out.read()
                finally:
                    out.close()
            finally:
                if server is not None:
                    try:
                        server.stop()
                    except Exception:
                        pass
                if upstream is not None:
                    try:
                        upstream.stop()
                    except Exception:
                        pass
                if old_env is None:
                    os.environ.pop("FETCHURL_SERVER", None)
                else:
                    os.environ["FETCHURL_SERVER"] = old_env
                try:
                    net.remove()
                except Exception:
                    pass

            self.assertEqual(fetched, content)
        if hasattr(signal, "SIGALRM"):
            signal.alarm(0)
            signal.signal(signal.SIGALRM, old_handler)
