"""Integration test for Python SDK using Testcontainers."""

import hashlib
import os
import tempfile
import unittest
from pathlib import Path

import fetchurl
from testcontainers.core.container import DockerfileContainer, GenericContainer
from testcontainers.core.network import Network
from testcontainers.core.waiting_utils import wait_for_logs


def sha256hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class TestIntegration(unittest.TestCase):
    def test_fetchurl_server_integration(self):
        content = b"integration-test"
        hash_hex = sha256hex(content)

        repo_root = Path(__file__).resolve().parents[2]
        old_env = os.environ.get("FETCHURL_SERVER")

        with tempfile.TemporaryDirectory() as tmpdir:
            data_dir = Path(tmpdir)
            (data_dir / "file").write_bytes(content)

            with Network() as net:
                upstream = (
                    GenericContainer("python:3.12-alpine")
                    .with_network(net)
                    .with_network_aliases("upstream")
                    .with_bind_mount(str(data_dir), "/srv", mode="ro")
                    .with_command(
                        [
                            "python",
                            "-m",
                            "http.server",
                            "8000",
                            "--bind",
                            "0.0.0.0",
                            "--directory",
                            "/srv",
                        ]
                    )
                )
                upstream.start()
                wait_for_logs(upstream, "Serving HTTP on", timeout=10)

                image_ref = os.environ.get("FETCHURL_TEST_IMAGE")
                if image_ref:
                    server = (
                        GenericContainer(image_ref)
                        .with_command(["server"])
                        .with_network(net)
                        .with_exposed_ports(8080)
                    )
                else:
                    server = (
                        DockerfileContainer(str(repo_root))
                        .with_command("server")
                        .with_network(net)
                        .with_exposed_ports(8080)
                    )
                server.start()
                wait_for_logs(server, "Starting server", timeout=20)

                out = tempfile.TemporaryFile()
                try:
                    host = server.get_container_host_ip()
                    port = server.get_exposed_port(8080)
                    os.environ["FETCHURL_SERVER"] = f"\"http://{host}:{port}\""

                    fetchurl.fetch(
                        fetchurl.UrllibFetcher(),
                        "sha256",
                        hash_hex,
                        ["http://upstream:8000/file"],
                        out=out,
                    )
                    out.seek(0)
                    fetched = out.read()
                finally:
                    out.close()
                    server.stop()
                    upstream.stop()
                    if old_env is None:
                        os.environ.pop("FETCHURL_SERVER", None)
                    else:
                        os.environ["FETCHURL_SERVER"] = old_env

            self.assertEqual(fetched, content)
