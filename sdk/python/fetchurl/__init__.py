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
    """Base exception for fetchurl SDK. All SDK-specific errors inherit from this."""


class UnsupportedAlgorithmError(FetchUrlError):
    """Raised during initialization if the provided hash algorithm is not in the whitelist (e.g., md5).
    This prevents unnecessary network requests for algorithms the server or client cannot verify.
    """

    def __init__(self, algo: str):
        self.algo = algo
        super().__init__(f"unsupported algorithm: {algo}")


class HashMismatchError(FetchUrlError):
    """Triggered post-download when the computed hash of the response body differs from the requested hash.
    Indicates potential data corruption in transit or cache poisoning.
    """

    def __init__(self, expected: str, actual: str):
        self.expected = expected
        self.actual = actual
        super().__init__(f"hash mismatch: expected {expected}, got {actual}")


class AllSourcesFailedError(FetchUrlError):
    """Raised when the client exhausts all configured cache servers and direct source URLs without a successful fetch.
    Wraps the last encountered network or validation error.
    """

    def __init__(self, last_error: Exception | None = None):
        self.last_error = last_error
        super().__init__("all sources failed")


class PartialWriteError(FetchUrlError):
    """Raised if an error occurs mid-stream after some bytes have already been written to the destination.
    Signals to the caller that the output stream is tainted and cannot be reused for fallback attempts.
    """

    def __init__(self, cause: Exception):
        self.cause = cause
        super().__init__(f"partial write: {cause}")


class MissingSourceUrlsError(FetchUrlError):
    """Raised during session setup if no fallback direct URLs are provided.
    The spec requires at least one source URL to guarantee availability if the cache misses.
    """

    def __init__(self):
        super().__init__("source_urls is required")


# --- Algorithm helpers ---

_SUPPORTED_ALGOS = {"sha1", "sha256", "sha512"}


def normalize_algo(name: str) -> str:
    """Normalizes an algorithm identifier to ensure cache directory consistency.
    Converts inputs like 'SHA-256' to 'sha256' by removing non-alphanumeric characters and lowercasing.
    """
    return re.sub(r"[^a-z0-9]", "", name.lower())


def is_supported(algo: str) -> bool:
    """Verifies whether the normalized algorithm name is supported by the client implementation.
    Restricts usage to known-safe algorithms (e.g. sha1, sha256, sha512) to prevent unsupported hash attempts.
    """
    return normalize_algo(algo) in _SUPPORTED_ALGOS


# --- SFV helpers (RFC 8941 string lists) ---


def encode_source_urls(urls: list[str]) -> str:
    """Serializes a list of URLs into an RFC 8941 Structured Field Value (SFV) string list format.
    This is necessary to safely transmit multiple source URLs in the `X-Source-Urls` HTTP header, escaping characters as required.
    """
    return ", ".join(
        '"' + url.replace("\\", "\\\\").replace('"', '\\"') + '"' for url in urls
    )


def parse_fetchurl_server(value: str) -> list[str]:
    """Deserializes an RFC 8941 SFV string list representing configured cache servers.
    Used primarily to parse the `FETCHURL_SERVER` environment variable, handling spaces and escaped quotes.
    """
    value = value.strip()
    if not value:
        return []
    if not value.startswith('"'):
        return [value]
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
        if not source_urls:
            raise MissingSourceUrlsError()

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
            url = f"{base}/{algo}/{hash}"
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
    """Synchronous HTTP client protocol acting as an adapter for underlying HTTP libraries.

    Enables dependency injection for the `fetch` function, abstracting the actual HTTP request mechanism.
    Must return an unbuffered readable stream to allow incremental processing without loading the entire payload into memory.

    Example with requests::

        class RequestsFetcher:
            def get(self, url, headers):
                resp = requests.get(url, headers=headers, stream=True)
                return (resp.status_code, resp.raw)
    """

    def get(self, url: str, headers: dict[str, str]) -> tuple[int, BinaryIO]:
        """Executes a GET request. Returns a tuple of the HTTP status code and a raw binary stream of the response body."""
        ...


@runtime_checkable
class AsyncFetcher(Protocol):
    """Asynchronous HTTP client protocol acting as an adapter for underlying async HTTP libraries.

    Provides dependency injection for the `async_fetch` function.
    Requires returning an asynchronous iterator yielding byte chunks to facilitate non-blocking incremental hashing.

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
        """Executes an async GET request. Returns the HTTP status code and an asynchronous iterator of byte chunks."""
        ...


# --- UrllibFetcher (stdlib, zero deps) ---


class UrllibFetcher:
    """A synchronous Fetcher implementation utilizing Python's built-in `urllib.request`.
    Serves as the zero-dependency default client, ensuring the SDK functions out-of-the-box without external libraries.
    """

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
    """Orchestrates the synchronous fetching and validation protocol.

    It sequentially attempts to retrieve content from configured cache servers, followed by the provided source URLs.
    Successfully downloaded content is continuously piped to the `out` stream while being incrementally hashed.
    Side effect: writes directly to the `out` object. If execution fails midway, `out` contains a partial payload.

    Raises:
        PartialWriteError: When the connection drops or validation fails after writing initial bytes.
        AllSourcesFailedError: When all fallback options are exhausted.
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
    """Orchestrates the asynchronous fetching and validation protocol.

    Operates identically to `fetch` but utilizes the `AsyncFetcher` protocol for non-blocking I/O.
    The response body is streamed into the synchronous `out` writer while incrementally hashed.
    Side effect: writes directly to the `out` object, potentially leaving a partial write on failure.

    Raises:
        PartialWriteError: When the connection drops or validation fails after writing initial bytes.
        AllSourcesFailedError: When all fallback options are exhausted.
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
