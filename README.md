# fetchurl

Reference **server** and CLI for the [fetchurl protocol](https://github.com/fetchurl/spec): a simple content-addressable URL cache for CI and package managers.

## Protocol

Normative specification: **[fetchurl/spec](https://github.com/fetchurl/spec)** (`SPEC.md`).

This repository implements the server (and a small CLI). It is **not** the protocol source of truth.

## Clients (SDKs)

| Language | Repository |
|----------|------------|
| JavaScript | [fetchurl/sdk-js](https://github.com/fetchurl/sdk-js) |
| Python | [fetchurl/sdk-python](https://github.com/fetchurl/sdk-python) |
| Rust | [fetchurl/sdk-rust](https://github.com/fetchurl/sdk-rust) |

## Run

```bash
go run ./cmd/fetchurl server
# or build / use Docker — see Dockerfile
```

Configure storage and listen address via the CLI/server flags and env (see `cmd/fetchurl` and `internal/app`).

Clients reach the server via `FETCHURL_SERVER` (full base URL ready to append `/:algo/:hash`), per the [spec](https://github.com/fetchurl/spec/blob/main/SPEC.md).

## Install (Go)

```bash
go install github.com/fetchurl/fetchurl/cmd/fetchurl@latest
```

Module path: `github.com/fetchurl/fetchurl`.

## Container image

Published by CI as **`ghcr.io/fetchurl/fetchurl`** (also tagged with release versions).

For local/SDK integration tests you can still build a local tag:

```bash
docker build -t fetchurl:local .
# or use the published image:
# FETCHURL_TEST_IMAGE=ghcr.io/fetchurl/fetchurl:latest
```

## Development

```bash
go test ./...
```

## License

MIT — see [LICENSE](./LICENSE).
