# Motivation

- Repeated downloads in CI environment
- Lack of standard in content addressable caching (we have pnpm and stuff but nothing standard that can be used by everyone)
- CI caching is mostly push based: build happens, artifacts are pushed based on a key
- Caching codes defined by intention instead of content (nix: hash of derivation, npm: cache of lockfile)

# Alternatives
- Backup the downloaded assets as workflow cache: looks very wasteful and loses value when anything changes (like the cache key is the hash of package-lock.json
- Caching proxy: too much hassle to setup a MITM proxy with a custom cert and force traffic through it
- Let it redownload assets every time

# Parts
- Client: the one that wants a file (npm, uv, hex, Nix)
- Server: the server proposed by this document, intermediates downloads, stores confirmed files until eviction
- Upstream: a daisy-chained server
- Source: the one that would directly serve the file to the client otherwise

# Scope
- Algorithms: sha1, sha256, sha512
- Only public data (no auth)
- Focus in caching package manager dependencies (ex: npm packages)

# Design

```
GET /api/fetchurl/sha256/e3b0... HTTP/1.1
X-Source-Urls: https://cdn1.com/file.tar.gz, https://backup.org/archive.tgz
```
- The server MUST only work with public, or well hidden, data
- `X-Source-Urls` MUST define as a list of source URLs following [RFC 8941](https://www.rfc-editor.org/rfc/rfc8941.html#name-lists)
- A source URL SHOULD be chosen randomly at fetch time, like a mirror list
- The server SHOULD NOT retry or fallback sources at fetch time as the content already starts flowing to clients when the channel to the source is already confirmed
- The `FETCHURL_SERVER` MUST define a list of servers following [RFC 8941](https://www.rfc-editor.org/rfc/rfc8941.html#name-lists) if the first character is a ", otherwise the whole variable is interpreted as one single value
- The `FETCHURL_SERVER` environment variable MUST have the full URLs ready to append `/:algo/:hash`
- The `FETCHURL_SERVER` environment variable CAN be absent or empty, which MUST disable server support
- Clients are instructed by environment to use servers by using the `FETCHURL_SERVER` environment variable
- Clients MUST check for `FETCHURL_SERVER`
- Clients CAN fall back to direct download if something goes wrong
- Clients MUST know the hash and source URLs before connecting to a server
- This design doesn't cover how a client may get the source URLs and hashes of the content
- The server can delete any item at any moment for any reason
- The process of deletion and addition of a cache item MUST be atomic
- The source MUST provide content size. If not provided, the server MUST reject the request
- The server CAN start serving the data while it's checking for the hash to optimize time to first byte
- If the hash doesn't match at the end of the stream the server MUST abruptly close the connection
- The client MUST only accept the file if the connection ended gracefully, anything that resembles a failure MUST be considered as a rejection
- The servers CAN be daisy-chained and query upstream servers for items.
- On daisy-chaining, any server that implements this spec can be used as an upstream.
- Servers CAN evict any data at any time and have their own independent eviction policies to have the best cache hit vs resource usage tradeoff
- When saving data on disk, the data directory SHOULD follow `/:algo/:shard/:hash` where shard is the first n letters of the hash where n by default is 2
- Hashing algorithms MUST be defined as their names in lowercase discarding letters which don't match with `[a-z0-9]`. Examples: md5, sha1, sha256, sha512
- Clients MUST prefer the sha256 algorithm if they have the option

# Challenges
- Implementation: this repository holds a reference implementation, specialized implementations can be made to, for example, run on Cloudflare Workers or be implemented in Rust or Elixir for scalability and performance
- Adoption: implement logic on different clients to use a server
- Adoption: CI providers exposing servers in build environments and having out of the box support
- Performance: efficient way to evict automatically data based on a policy, eviction is one atomic rm, keeping track of stuff is another problem
