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

## Development

```bash
go test ./...
# Docker image for SDK integration tests elsewhere:
# docker build -t fetchurl:local .
```

## License

MIT — see [LICENSE](./LICENSE).
