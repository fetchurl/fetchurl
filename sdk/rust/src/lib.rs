//! Fetchurl SDK for Rust.
//!
//! Protocol-level client for fetchurl content-addressable cache servers.
//! This crate does not perform HTTP requests directly — it provides
//! a state machine ([`FetchSession`]) that drives the protocol logic while
//! the caller handles I/O with any HTTP library.
//!
//! # Example
//!
//! ```no_run
//! use std::io;
//! use fetchurl_sdk as fetchurl;
//!
//! let source_urls = vec!["https://cdn.example.com/file.tar.gz"];
//!
//! let mut session = fetchurl::FetchSession::new(
//!     "sha256", "e3b0c44...", &source_urls,
//! ).unwrap();
//!
//! while let Some(attempt) = session.next_attempt() {
//!     // Make HTTP GET to attempt.url() with attempt.headers()
//!     // using your preferred HTTP library.
//!     //
//!     // On success: stream body through session.verifier(writer),
//!     //   call verifier.finish(), then session.report_success().
//!     // On failure after bytes written: session.report_partial().
//!     // On failure before any bytes: just continue the loop.
//! }
//! ```

use std::io::{self, Write};

use digest::Digest;
use rand::seq::SliceRandom;

/// Errors returned by the fetchurl SDK.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// The requested hash algorithm is not supported (sha1, sha256, sha512).
    #[error("unsupported algorithm: {0}")]
    UnsupportedAlgorithm(String),

    /// Source URLs are required by the protocol.
    #[error("source_urls is required")]
    MissingSourceUrls,

    /// The content hash does not match the expected hash.
    #[error("hash mismatch: expected {expected}, got {actual}")]
    HashMismatch { expected: String, actual: String },
}

/// Normalize a hash algorithm name per the fetchurl spec:
/// lowercase, keeping only `[a-z0-9]`.
///
/// Examples: `"SHA-256"` → `"sha256"`, `"SHA512"` → `"sha512"`
pub fn normalize_algo(name: &str) -> String {
    name.chars()
        .filter_map(|c| match c {
            'A'..='Z' => Some(c.to_ascii_lowercase()),
            'a'..='z' | '0'..='9' => Some(c),
            _ => None,
        })
        .collect()
}

/// Check if a hash algorithm is supported.
pub fn is_supported(algo: &str) -> bool {
    matches!(normalize_algo(algo).as_str(), "sha1" | "sha256" | "sha512")
}

/// Parse the `FETCHURL_SERVER` environment variable value (an RFC 8941 string list).
pub fn parse_fetchurl_server(value: &str) -> Vec<String> {
    let value = value.trim();
    if value.is_empty() {
        return Vec::new();
    }
    if !value.starts_with('"') {
        return vec![value.to_string()];
    }
    parse_sfv_string_list(value)
}

/// Encode source URLs as an RFC 8941 string list for the `X-Source-Urls` header.
pub fn encode_source_urls(urls: &[impl AsRef<str>]) -> String {
    let strs: Vec<&str> = urls.iter().map(|s| s.as_ref()).collect();
    encode_sfv_string_list(&strs)
}

// --- SFV helpers (RFC 8941 string lists) ---

fn encode_sfv_string_list(strings: &[&str]) -> String {
    strings
        .iter()
        .map(|s| {
            let escaped = s.replace('\\', "\\\\").replace('"', "\\\"");
            format!("\"{escaped}\"")
        })
        .collect::<Vec<_>>()
        .join(", ")
}

fn parse_sfv_string_list(input: &str) -> Vec<String> {
    let mut results = Vec::new();
    let bytes = input.as_bytes();
    let mut i = 0;

    while i < bytes.len() {
        // Skip whitespace
        while i < bytes.len() && matches!(bytes[i], b' ' | b'\t') {
            i += 1;
        }
        if i >= bytes.len() {
            break;
        }

        // Expect opening quote for a string item
        if bytes[i] != b'"' {
            // Skip non-string content until comma or end
            while i < bytes.len() && bytes[i] != b',' {
                i += 1;
            }
            if i < bytes.len() {
                i += 1;
            }
            continue;
        }
        i += 1;

        // Parse string content
        let mut s = String::new();
        while i < bytes.len() {
            match bytes[i] {
                b'\\' if i + 1 < bytes.len() => {
                    s.push(bytes[i + 1] as char);
                    i += 2;
                }
                b'"' => {
                    i += 1;
                    break;
                }
                c => {
                    s.push(c as char);
                    i += 1;
                }
            }
        }
        results.push(s);

        // Skip parameters (;key=value) and whitespace until comma or end
        while i < bytes.len() && bytes[i] != b',' {
            i += 1;
        }
        if i < bytes.len() {
            i += 1;
        }
    }

    results
}

// --- FetchAttempt ---

/// A single fetch attempt, describing the URL to request and headers to set.
#[derive(Clone, Debug)]
pub struct FetchAttempt {
    url: String,
    headers: Vec<(String, String)>,
}

impl FetchAttempt {
    /// The URL to make a GET request to.
    pub fn url(&self) -> &str {
        &self.url
    }

    /// Headers to include in the request (e.g., `X-Source-Urls`).
    pub fn headers(&self) -> &[(String, String)] {
        &self.headers
    }
}

// --- FetchSession ---

/// Drives the fetchurl client protocol as a state machine.
///
/// Determines which URLs to try and in what order: servers first
/// (with source URLs forwarded as `X-Source-Urls`), then direct
/// source URLs in random order.
///
/// The caller iterates through attempts, makes HTTP requests with
/// their preferred library, and reports results back to the session.
pub struct FetchSession {
    attempts: Vec<FetchAttempt>,
    current: usize,
    algo: String,
    hash: String,
    done: bool,
    success: bool,
}

impl FetchSession {
    /// Create a new fetch session.
    ///
    /// - `algo`: hash algorithm name (e.g. `"sha256"`)
    /// - `hash`: expected hash in hex
    /// - `source_urls`: direct source URLs (tried after servers, in random order)
    pub fn new(algo: &str, hash: &str, source_urls: &[impl AsRef<str>]) -> Result<Self, Error> {
        if source_urls.is_empty() {
            return Err(Error::MissingSourceUrls);
        }

        let algo = normalize_algo(algo);
        if !is_supported(&algo) {
            return Err(Error::UnsupportedAlgorithm(algo));
        }

        let servers_env = std::env::var("FETCHURL_SERVER").unwrap_or_default();
        let servers = parse_fetchurl_server(&servers_env);

        let source_header = if !source_urls.is_empty() {
            Some(encode_source_urls(source_urls))
        } else {
            None
        };

        let mut attempts = Vec::new();

        // Servers first
        for server in servers {
            let base = server.trim_end_matches('/');
            let url = format!("{base}/{algo}/{hash}");
            let mut headers = Vec::new();
            if let Some(ref val) = source_header {
                headers.push(("X-Source-Urls".to_string(), val.clone()));
            }
            attempts.push(FetchAttempt { url, headers });
        }

        // Direct sources (shuffled per spec)
        let mut direct: Vec<String> = source_urls.iter().map(|s| s.as_ref().to_string()).collect();
        direct.shuffle(&mut rand::thread_rng());
        for url in direct {
            attempts.push(FetchAttempt {
                url,
                headers: Vec::new(),
            });
        }

        Ok(FetchSession {
            attempts,
            current: 0,
            algo,
            hash: hash.to_string(),
            done: false,
            success: false,
        })
    }

    /// Get the next attempt to try.
    ///
    /// Returns `None` when all attempts are exhausted or the session is
    /// finished (after [`report_success`](Self::report_success) or
    /// [`report_partial`](Self::report_partial)).
    ///
    /// If the HTTP request fails without writing any bytes, just call
    /// `next_attempt()` again to try the next source.
    pub fn next_attempt(&mut self) -> Option<FetchAttempt> {
        if self.done || self.current >= self.attempts.len() {
            return None;
        }
        let attempt = self.attempts[self.current].clone();
        self.current += 1;
        Some(attempt)
    }

    /// Report that the current attempt succeeded. Stops the session.
    pub fn report_success(&mut self) {
        self.done = true;
        self.success = true;
    }

    /// Report that bytes were already written to the output before a failure.
    /// Stops the session — no further attempts since the output is tainted.
    pub fn report_partial(&mut self) {
        self.done = true;
    }

    /// Whether the session completed with a successful download.
    pub fn succeeded(&self) -> bool {
        self.success
    }

    /// Create a [`HashVerifier`] wrapping the given writer.
    ///
    /// Pipe the HTTP response body through the verifier, then call
    /// [`HashVerifier::finish`] to check the hash.
    pub fn verifier<W: Write>(&self, writer: W) -> HashVerifier<W> {
        HashVerifier::new(&self.algo, &self.hash, writer)
    }
}

// --- Hasher ---

enum HasherInner {
    Sha1(sha1::Sha1),
    Sha256(sha2::Sha256),
    Sha512(sha2::Sha512),
}

impl HasherInner {
    fn new(algo: &str) -> Option<Self> {
        match algo {
            "sha1" => Some(HasherInner::Sha1(sha1::Sha1::new())),
            "sha256" => Some(HasherInner::Sha256(sha2::Sha256::new())),
            "sha512" => Some(HasherInner::Sha512(sha2::Sha512::new())),
            _ => None,
        }
    }

    fn update(&mut self, data: &[u8]) {
        match self {
            HasherInner::Sha1(h) => h.update(data),
            HasherInner::Sha256(h) => h.update(data),
            HasherInner::Sha512(h) => h.update(data),
        }
    }

    fn finalize(self) -> Vec<u8> {
        match self {
            HasherInner::Sha1(h) => h.finalize().to_vec(),
            HasherInner::Sha256(h) => h.finalize().to_vec(),
            HasherInner::Sha512(h) => h.finalize().to_vec(),
        }
    }
}

fn to_hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

// --- HashVerifier ---

/// A writer wrapper that computes a hash of all written data and verifies
/// it against an expected hash when [`finish`](Self::finish) is called.
pub struct HashVerifier<W: Write> {
    inner: W,
    hasher: HasherInner,
    expected_hash: String,
    bytes_written: u64,
}

impl<W: Write> HashVerifier<W> {
    fn new(algo: &str, expected_hash: &str, inner: W) -> Self {
        let hasher = HasherInner::new(algo).expect("HashVerifier created with validated algo");
        HashVerifier {
            inner,
            hasher,
            expected_hash: expected_hash.to_string(),
            bytes_written: 0,
        }
    }

    /// Number of bytes written to the inner writer so far.
    pub fn bytes_written(&self) -> u64 {
        self.bytes_written
    }

    /// Finalize the hash and verify it matches the expected value.
    ///
    /// Returns the inner writer on success, or [`Error::HashMismatch`] on failure.
    pub fn finish(self) -> Result<W, Error> {
        let actual = to_hex(&self.hasher.finalize());
        if actual == self.expected_hash {
            Ok(self.inner)
        } else {
            Err(Error::HashMismatch {
                expected: self.expected_hash,
                actual,
            })
        }
    }
}

impl<W: Write> Write for HashVerifier<W> {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let n = self.inner.write(buf)?;
        self.hasher.update(&buf[..n]);
        self.bytes_written += n as u64;
        Ok(n)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sha256_hex(data: &[u8]) -> String {
        to_hex(&sha2::Sha256::digest(data))
    }

    #[test]
    fn test_normalize_algo() {
        assert_eq!(normalize_algo("SHA-256"), "sha256");
        assert_eq!(normalize_algo("sha256"), "sha256");
        assert_eq!(normalize_algo("SHA512"), "sha512");
        assert_eq!(normalize_algo("md5"), "md5");
    }

    #[test]
    fn test_is_supported() {
        assert!(is_supported("sha256"));
        assert!(is_supported("SHA-256"));
        assert!(is_supported("sha1"));
        assert!(is_supported("sha512"));
        assert!(!is_supported("md5"));
    }

    #[test]
    fn test_sfv_encode() {
        assert_eq!(
            encode_sfv_string_list(&["https://a.com", "https://b.com"]),
            r#""https://a.com", "https://b.com""#
        );
    }

    #[test]
    fn test_sfv_parse() {
        let parsed = parse_sfv_string_list(r#""https://a.com", "https://b.com""#);
        assert_eq!(parsed, vec!["https://a.com", "https://b.com"]);
    }

    #[test]
    fn test_sfv_roundtrip() {
        let urls = &[
            "https://cdn.example.com/file.tar.gz",
            "https://mirror.org/archive.tgz",
        ];
        let encoded = encode_sfv_string_list(urls);
        let decoded = parse_sfv_string_list(&encoded);
        assert_eq!(decoded, urls);
    }

    #[test]
    fn test_sfv_parse_with_parameters() {
        // Parameters should be ignored, only the string value matters
        let parsed = parse_sfv_string_list(r#""https://a.com";q=0.9, "https://b.com""#);
        assert_eq!(parsed, vec!["https://a.com", "https://b.com"]);
    }

    #[test]
    fn test_hash_verifier_success() {
        let data = b"hello world";
        let hash = sha256_hex(data);

        let mut output = Vec::new();
        {
            let mut verifier = HashVerifier::new("sha256", &hash, &mut output);
            verifier.write_all(data).unwrap();
            assert_eq!(verifier.bytes_written(), data.len() as u64);
            verifier.finish().unwrap();
        }
        assert_eq!(output, data);
    }

    #[test]
    fn test_hash_verifier_mismatch() {
        let data = b"hello world";
        let wrong_hash = sha256_hex(b"wrong");

        let mut output = Vec::new();
        let mut verifier = HashVerifier::new("sha256", &wrong_hash, &mut output);
        verifier.write_all(data).unwrap();
        let err = verifier.finish().unwrap_err();
        assert!(matches!(err, Error::HashMismatch { .. }));
    }

    #[test]
    fn test_session_unsupported_algo() {
        let err = FetchSession::new("md5", "abc", &["http://src"]);
        assert!(matches!(err, Err(Error::UnsupportedAlgorithm(_))));
    }

    #[test]
    fn test_session_missing_source_urls() {
        let err = FetchSession::new("sha256", "abc", &[] as &[&str]);
        assert!(matches!(err, Err(Error::MissingSourceUrls)));
    }

    #[test]
    fn test_session_attempt_ordering() {
        let hash = sha256_hex(b"test");
        unsafe {
            std::env::set_var(
                "FETCHURL_SERVER",
                "\"http://cache1/api/fetchurl\", \"http://cache2/api/fetchurl\"",
            );
        }
        let mut session = FetchSession::new("sha256", &hash, &["http://src1"]).unwrap();

        // First two attempts should be servers
        let a1 = session.next_attempt().unwrap();
        assert!(a1.url().starts_with("http://cache1/api/fetchurl/sha256/"));
        assert!(!a1.headers().is_empty());

        let a2 = session.next_attempt().unwrap();
        assert!(a2.url().starts_with("http://cache2/api/fetchurl/sha256/"));

        // Third should be direct source
        let a3 = session.next_attempt().unwrap();
        assert_eq!(a3.url(), "http://src1");
        assert!(a3.headers().is_empty());

        // No more
        assert!(session.next_attempt().is_none());
        assert!(!session.succeeded());
    }

    #[test]
    fn test_session_success_stops() {
        let hash = sha256_hex(b"test");
        unsafe {
            std::env::set_var("FETCHURL_SERVER", "\"http://cache/api/fetchurl\"");
        }
        let mut session = FetchSession::new("sha256", &hash, &["http://src"]).unwrap();

        let _ = session.next_attempt().unwrap();
        session.report_success();
        assert!(session.succeeded());
        assert!(session.next_attempt().is_none());
    }

    #[test]
    fn test_session_partial_stops() {
        let hash = sha256_hex(b"test");
        unsafe {
            std::env::set_var("FETCHURL_SERVER", "\"http://cache/api/fetchurl\"");
        }
        let mut session = FetchSession::new("sha256", &hash, &["http://src"]).unwrap();

        let _ = session.next_attempt().unwrap();
        session.report_partial();
        assert!(!session.succeeded());
        assert!(session.next_attempt().is_none());
    }

    #[test]
    fn test_session_server_has_source_header() {
        let hash = sha256_hex(b"test");
        unsafe {
            std::env::set_var("FETCHURL_SERVER", "\"http://cache/api/fetchurl\"");
        }
        let mut session =
            FetchSession::new("sha256", &hash, &["http://src1", "http://src2"]).unwrap();

        let attempt = session.next_attempt().unwrap();
        let source_header = attempt
            .headers()
            .iter()
            .find(|(k, _)| k == "X-Source-Urls")
            .map(|(_, v)| v.clone())
            .unwrap();

        let parsed = parse_sfv_string_list(&source_header);
        assert!(parsed.contains(&"http://src1".to_string()));
        assert!(parsed.contains(&"http://src2".to_string()));
    }
}
