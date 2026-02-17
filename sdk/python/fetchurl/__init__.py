"""Fetchurl SDK for Python.

Protocol-level client for fetchurl content-addressable cache servers.
Works with any HTTP library through the Fetcher/AsyncFetcher protocols.

Zero dependencies — uses only the Python standard library.

Three levels of usage:

  # 1. One-liner with stdlib
  fetchurl.fetch(UrllibFetcher(), "sha256", hash, urls, output)

  # 2. Custom HTTP client — implement the Fetcher protocol
  class MyFetcher:
      def get(self, url, headers):
          resp = requests.get(url, headers=headers, stream=True)
          return (resp.status_code, resp.raw)

  fetchurl.fetch(MyFetcher(), "sha256", hash, urls, output)

  # 3. Low-level — drive the state machine yourself
  session = FetchSession("sha256", hash, urls)
  while attempt := session.next_attempt():
      # make HTTP request with whatever library you want
      ...
"""

from __future__ import annotations

import hashlib
import os
import random
import re
from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from typing import BinaryIO, Protocol, runtime_checkable


# --- Errors ---


class FetchUrlError(Exception):
    """Base exception for fetchurl SDK."""


class UnsupportedAlgorithmError(FetchUrlError):
    """The requested hash algorithm is not supported."""

    def __init__(self, algo: str):
        self.algo = algo
        super().__init__(f"unsupported algorithm: {algo}")


class HashMismatchError(FetchUrlError):
    """The content hash does not match the expected hash."""

    def __init__(self, expected: str, actual: str):
        self.expected = expected
        self.actual = actual
        super().__init__(f"hash mismatch: expected {expected}, got {actual}")


class AllSourcesFailedError(FetchUrlError):
    """All servers and sources failed to provide the content."""

    def __init__(self, last_error: Exception | None = None):
        self.last_error = last_error
        super().__init__("all sources failed")


class PartialWriteError(FetchUrlError):
    """Bytes were written before failure; output is tainted."""

    def __init__(self, cause: Exception):
        self.cause = cause
        super().__init__(f"partial write: {cause}")


# --- Algorithm helpers ---

_SUPPORTED_ALGOS = {"sha1", "sha256", "sha512"}


def normalize_algo(name: str) -> str:
    """Normalize algorithm name per spec: lowercase, only [a-z0-9]."""
    return re.sub(r"[^a-z0-9]", "", name.lower())


def is_supported(algo: str) -> bool:
    """Check if a hash algorithm is supported."""
    return normalize_algo(algo) in _SUPPORTED_ALGOS


# --- SFV helpers (RFC 8941 string lists) ---


def encode_source_urls(urls: list[str]) -> str:
    """Encode URLs as an RFC 8941 string list for X-Source-Urls header."""
    return ", ".join(
        '"' + url.replace("\\", "\\\\").replace('"', '\\"') + '"' for url in urls
    )


def parse_fetchurl_server(value: str) -> list[str]:
    """Parse FETCHURL_SERVER env var (RFC 8941 string list)."""
    results: list[str] = []
    i = 0
    while i < len(value):
        while i < len(value) and value[i] in " \t":
            i += 1
        if i >= len(value):
            break
        if value[i] != '"':
            while i < len(value) and value[i] != ",":
                i += 1
            if i < len(value):
                i += 1
            continue
        i += 1
        s: list[str] = []
        while i < len(value):
            if value[i] == "\\" and i + 1 < len(value):
                s.append(value[i + 1])
                i += 2
            elif value[i] == '"':
                i += 1
                break
            else:
                s.append(value[i])
                i += 1
        results.append("".join(s))
        while i < len(value) and value[i] != ",":
            i += 1
        if i < len(value):
            i += 1
    return results


# --- FetchAttempt ---


@dataclass(frozen=True)
class FetchAttempt:
    """A single fetch attempt with URL and headers."""

    url: str
    headers: dict[str, str] = field(default_factory=dict)


# --- HashVerifier ---


class HashVerifier:
    """Wraps a binary writer, computes hash, verifies on finish().

    Usage::

        verifier = session.verifier(output_file)
        while chunk := body.read(65536):
            verifier.write(chunk)
        verifier.finish()  # raises HashMismatchError on failure
    """

    def __init__(self, algo: str, expected_hash: str, writer: BinaryIO):
        self._writer = writer
        self._hasher = hashlib.new(normalize_algo(algo))
        self._expected = expected_hash
        self._bytes_written = 0

    @property
    def bytes_written(self) -> int:
        return self._bytes_written

    def write(self, data: bytes) -> int:
        n = self._writer.write(data)
        if n is None:
            n = len(data)
        self._hasher.update(data[:n])
        self._bytes_written += n
        return n

    def finish(self) -> None:
        """Verify hash. Raises HashMismatchError on failure."""
        actual = self._hasher.hexdigest()
        if actual != self._expected:
            raise HashMismatchError(self._expected, actual)


# --- FetchSession ---


class FetchSession:
    """State machine driving the fetchurl client protocol.

    Servers are tried first (with X-Source-Urls header forwarded),
    then direct source URLs in random order per spec.

    The caller iterates through attempts, makes HTTP requests
    with their preferred library, and reports results back::

        session = FetchSession(servers, "sha256", hash, source_urls)
        while attempt := session.next_attempt():
            # attempt.url and attempt.headers tell you what to request
            ...
            session.report_success()  # or report_partial()
    """

    def __init__(
        self,
        algo: str,
        hash: str,
        source_urls: list[str],
    ):
        servers = parse_fetchurl_server(os.environ.get("FETCHURL_SERVER", ""))
        algo = normalize_algo(algo)
        if not is_supported(algo):
            raise UnsupportedAlgorithmError(algo)

        self._algo = algo
        self._hash = hash
        self._done = False
        self._success = False
        self._attempts: list[FetchAttempt] = []
        self._current = 0

        source_header = encode_source_urls(source_urls) if source_urls else None

        for server in servers:
            base = server.rstrip("/")
            url = f"{base}/api/fetchurl/{algo}/{hash}"
            headers: dict[str, str] = {}
            if source_header:
                headers["X-Source-Urls"] = source_header
            self._attempts.append(FetchAttempt(url=url, headers=headers))

        direct = list(source_urls)
        random.shuffle(direct)
        for url in direct:
            self._attempts.append(FetchAttempt(url=url))

    def next_attempt(self) -> FetchAttempt | None:
        """Get the next attempt, or None if session is finished.

        If an attempt fails without writing bytes, just call next_attempt() again.
        """
        if self._done or self._current >= len(self._attempts):
            return None
        attempt = self._attempts[self._current]
        self._current += 1
        return attempt

    def report_success(self) -> None:
        """Mark the session as successful. Stops further attempts."""
        self._done = True
        self._success = True

    def report_partial(self) -> None:
        """Mark that bytes were written before failure. Stops further attempts."""
        self._done = True

    def succeeded(self) -> bool:
        return self._success

    def verifier(self, writer: BinaryIO) -> HashVerifier:
        """Create a HashVerifier for this session's algo and expected hash."""
        return HashVerifier(self._algo, self._hash, writer)


# --- Fetcher protocols ---


@runtime_checkable
class Fetcher(Protocol):
    """Sync HTTP client protocol.

    Implement this to plug in any HTTP library.

    Example with requests::

        class RequestsFetcher:
            def get(self, url, headers):
                resp = requests.get(url, headers=headers, stream=True)
                return (resp.status_code, resp.raw)
    """

    def get(self, url: str, headers: dict[str, str]) -> tuple[int, BinaryIO]:
        """Make a GET request. Returns (status_code, readable_body)."""
        ...


@runtime_checkable
class AsyncFetcher(Protocol):
    """Async HTTP client protocol.

    Implement this to plug in any async HTTP library.

    Example with aiohttp::

        class AiohttpFetcher:
            def __init__(self):
                self._session = aiohttp.ClientSession()

            async def get(self, url, headers):
                resp = await self._session.get(url, headers=headers)
                return (resp.status, resp.content.iter_chunked(65536))
    """

    async def get(
        self, url: str, headers: dict[str, str]
    ) -> tuple[int, AsyncIterator[bytes]]:
        """Make a GET request. Returns (status_code, async_body_chunks)."""
        ...


# --- UrllibFetcher (stdlib, zero deps) ---


class UrllibFetcher:
    """Fetcher using urllib.request (stdlib, zero dependencies)."""

    def get(self, url: str, headers: dict[str, str]) -> tuple[int, BinaryIO]:
        import urllib.error
        import urllib.request

        req = urllib.request.Request(url, headers=headers)
        try:
            resp = urllib.request.urlopen(req)
            return (resp.status, resp)
        except urllib.error.HTTPError as e:
            return (e.code, e)


# --- Convenience functions ---

_CHUNK_SIZE = 64 * 1024


def fetch(
    fetcher: Fetcher,
    algo: str,
    hash: str,
    source_urls: list[str],
    out: BinaryIO,
) -> None:
    """High-level sync fetch. Handles the full protocol loop.

    Raises AllSourcesFailedError or PartialWriteError on failure.
    """
    session = FetchSession(algo, hash, source_urls)
    last_error: Exception | None = None

    while attempt := session.next_attempt():
        try:
            status, body = fetcher.get(attempt.url, dict(attempt.headers))
        except Exception as e:
            last_error = e
            continue

        if status != 200:
            last_error = Exception(f"unexpected status {status}")
            continue

        verifier = session.verifier(out)
        try:
            while chunk := body.read(_CHUNK_SIZE):
                verifier.write(chunk)
            verifier.finish()
            session.report_success()
            return
        except Exception as e:
            last_error = e
            if verifier.bytes_written > 0:
                session.report_partial()
                raise PartialWriteError(e) from e

    raise AllSourcesFailedError(last_error)


async def async_fetch(
    fetcher: AsyncFetcher,
    algo: str,
    hash: str,
    source_urls: list[str],
    out: BinaryIO,
) -> None:
    """High-level async fetch. Handles the full protocol loop.

    Raises AllSourcesFailedError or PartialWriteError on failure.
    """
    session = FetchSession(algo, hash, source_urls)
    last_error: Exception | None = None

    while attempt := session.next_attempt():
        try:
            status, chunks = await fetcher.get(attempt.url, dict(attempt.headers))
        except Exception as e:
            last_error = e
            continue

        if status != 200:
            last_error = Exception(f"unexpected status {status}")
            continue

        verifier = session.verifier(out)
        try:
            async for chunk in chunks:
                verifier.write(chunk)
            verifier.finish()
            session.report_success()
            return
        except Exception as e:
            last_error = e
            if verifier.bytes_written > 0:
                session.report_partial()
                raise PartialWriteError(e) from e

    raise AllSourcesFailedError(last_error)
