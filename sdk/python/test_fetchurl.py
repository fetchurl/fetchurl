"""Tests for fetchurl SDK."""

import hashlib
import io
import os
import unittest
from http.server import HTTPServer, BaseHTTPRequestHandler
from threading import Thread
from unittest.mock import patch

import fetchurl


def sha256hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class TestNormalizeAlgo(unittest.TestCase):
    def test_lowercase(self):
        self.assertEqual(fetchurl.normalize_algo("SHA-256"), "sha256")

    def test_already_normalized(self):
        self.assertEqual(fetchurl.normalize_algo("sha256"), "sha256")

    def test_strips_non_alnum(self):
        self.assertEqual(fetchurl.normalize_algo("SHA_512"), "sha512")


class TestIsSupported(unittest.TestCase):
    def test_supported(self):
        self.assertTrue(fetchurl.is_supported("sha256"))
        self.assertTrue(fetchurl.is_supported("SHA-256"))
        self.assertTrue(fetchurl.is_supported("sha1"))
        self.assertTrue(fetchurl.is_supported("sha512"))

    def test_unsupported(self):
        self.assertFalse(fetchurl.is_supported("md5"))


class TestSFV(unittest.TestCase):
    def test_encode(self):
        self.assertEqual(
            fetchurl.encode_source_urls(["https://a.com", "https://b.com"]),
            '"https://a.com", "https://b.com"',
        )

    def test_parse(self):
        parsed = fetchurl.parse_fetchurl_server('"https://a.com", "https://b.com"')
        self.assertEqual(parsed, ["https://a.com", "https://b.com"])

    def test_roundtrip(self):
        urls = ["https://cdn.example.com/f.tar.gz", "https://mirror.org/a.tgz"]
        encoded = fetchurl.encode_source_urls(urls)
        decoded = fetchurl.parse_fetchurl_server(encoded)
        self.assertEqual(decoded, urls)

    def test_parse_with_params(self):
        parsed = fetchurl.parse_fetchurl_server('"https://a.com";q=0.9, "https://b.com"')
        self.assertEqual(parsed, ["https://a.com", "https://b.com"])

    def test_empty(self):
        self.assertEqual(fetchurl.parse_fetchurl_server(""), [])


class TestHashVerifier(unittest.TestCase):
    def test_success(self):
        data = b"hello world"
        h = sha256hex(data)
        out = io.BytesIO()
        v = fetchurl.HashVerifier("sha256", h, out)
        v.write(data)
        self.assertEqual(v.bytes_written, len(data))
        v.finish()
        self.assertEqual(out.getvalue(), data)

    def test_mismatch(self):
        data = b"hello world"
        wrong_hash = sha256hex(b"wrong")
        out = io.BytesIO()
        v = fetchurl.HashVerifier("sha256", wrong_hash, out)
        v.write(data)
        with self.assertRaises(fetchurl.HashMismatchError) as ctx:
            v.finish()
        self.assertEqual(ctx.exception.expected, wrong_hash)


class TestFetchSession(unittest.TestCase):
    def test_missing_source_urls(self):
        with self.assertRaises(fetchurl.MissingSourceUrlsError):
            fetchurl.FetchSession("sha256", "abc", [])

    def test_unsupported_algo(self):
        with self.assertRaises(fetchurl.UnsupportedAlgorithmError):
            fetchurl.FetchSession("md5", "abc", ["http://src"])

    @patch.dict(os.environ, {"FETCHURL_SERVER": '"http://cache1", "http://cache2"'})
    def test_attempt_ordering(self):
        h = sha256hex(b"test")
        session = fetchurl.FetchSession(
            "sha256", h, ["http://src1"]
        )

        a1 = session.next_attempt()
        self.assertIsNotNone(a1)
        self.assertTrue(a1.url.startswith("http://cache1/api/fetchurl/sha256/"))
        self.assertIn("X-Source-Urls", a1.headers)

        a2 = session.next_attempt()
        self.assertTrue(a2.url.startswith("http://cache2/api/fetchurl/sha256/"))

        a3 = session.next_attempt()
        self.assertEqual(a3.url, "http://src1")
        self.assertEqual(a3.headers, {})

        self.assertIsNone(session.next_attempt())
        self.assertFalse(session.succeeded())

    @patch.dict(os.environ, {"FETCHURL_SERVER": '"http://cache"'})
    def test_success_stops(self):
        h = sha256hex(b"test")
        session = fetchurl.FetchSession("sha256", h, ["http://src"])
        session.next_attempt()
        session.report_success()
        self.assertTrue(session.succeeded())
        self.assertIsNone(session.next_attempt())

    @patch.dict(os.environ, {"FETCHURL_SERVER": '"http://cache"'})
    def test_partial_stops(self):
        h = sha256hex(b"test")
        session = fetchurl.FetchSession("sha256", h, ["http://src"])
        session.next_attempt()
        session.report_partial()
        self.assertFalse(session.succeeded())
        self.assertIsNone(session.next_attempt())


class TestFetch(unittest.TestCase):
    """Integration tests using a real HTTP server."""

    @staticmethod
    def _start_server(handler_class) -> tuple[HTTPServer, str]:
        server = HTTPServer(("127.0.0.1", 0), handler_class)
        port = server.server_address[1]
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        return server, f"http://127.0.0.1:{port}"

    def test_direct_download(self):
        content = b"test content"
        h = sha256hex(content)

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(content)

            def log_message(self, *args):
                pass

        server, url = self._start_server(Handler)
        try:
            out = io.BytesIO()
            # Empty servers (env var not set by default/or empty)
            with patch.dict(os.environ, {}, clear=True):
                fetchurl.fetch(fetchurl.UrllibFetcher(), "sha256", h, [url], out)
            self.assertEqual(out.getvalue(), content)
        finally:
            server.shutdown()

    def test_hash_mismatch_raises_partial(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"wrong content")

            def log_message(self, *args):
                pass

        server, url = self._start_server(Handler)
        try:
            out = io.BytesIO()
            with self.assertRaises(fetchurl.PartialWriteError):
                with patch.dict(os.environ, {}, clear=True):
                    fetchurl.fetch(
                        fetchurl.UrllibFetcher(), "sha256", sha256hex(b"right"), [url], out
                    )
        finally:
            server.shutdown()

    def test_all_sources_failed(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(404)
                self.end_headers()

            def log_message(self, *args):
                pass

        server, url = self._start_server(Handler)
        try:
            out = io.BytesIO()
            with self.assertRaises(fetchurl.AllSourcesFailedError):
                with patch.dict(os.environ, {}, clear=True):
                    fetchurl.fetch(
                        fetchurl.UrllibFetcher(), "sha256", sha256hex(b"x"), [url], out
                    )
        finally:
            server.shutdown()

    def test_server_fallback_to_direct(self):
        content = b"fallback content"
        h = sha256hex(content)

        class BadServer(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(500)
                self.end_headers()

            def log_message(self, *args):
                pass

        class GoodSource(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(content)

            def log_message(self, *args):
                pass

        bad, bad_url = self._start_server(BadServer)
        good, good_url = self._start_server(GoodSource)
        try:
            out = io.BytesIO()
            # Set server via env var
            with patch.dict(os.environ, {"FETCHURL_SERVER": f'"{bad_url}"'}):
                fetchurl.fetch(
                    fetchurl.UrllibFetcher(), "sha256", h, [good_url], out
                )
            self.assertEqual(out.getvalue(), content)
        finally:
            bad.shutdown()
            good.shutdown()


if __name__ == "__main__":
    unittest.main()
