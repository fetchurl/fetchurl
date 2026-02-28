# Consistently Ignored Patterns

## IGNORE: Unpinned Tool Versions

**- Pattern:** Using `latest`, `lts`, or version ranges instead of pinned versions for tools like `go`, `node`, `golangci-lint`, `actionlint` in `mise.toml`.
**- Justification:** Builds must be deterministic and reproducible. Unpinned versions lead to inconsistent environments and potential breakage.
**- Files Affected:** `mise.toml`

## IGNORE: Tool Version Downgrades

**- Pattern:** Lowering versions of linters (`golangci-lint`, `actionlint`) or runtimes (`go`, `node`) in `mise.toml` without explicit instruction.
**- Justification:** Causes loss of newer linting rules/features and potential build failures due to incompatible configuration syntax.
**- Files Affected:** `mise.toml`

## IGNORE: Mass Go Dependency Downgrades

**- Pattern:** Extensive changes to `go.mod` and `go.sum` that revert multiple transitive dependencies to older versions (often caused by running `go mod tidy` with an outdated Go toolchain).
**- Justification:** Introduces security vulnerabilities, breaks compatibility with newer code, and undoes previous dependency updates.
**- Files Affected:** `go.mod`, `go.sum`

## IGNORE: Explicit Error Suppression

**- Pattern:** Using `_ = f.Close()` or similar constructs to explicitly ignore errors, especially from `io.Closer` or `tx.Rollback()`. Also ignoring returns from `w.Write` or `fmt.Fprint`.
**- Justification:** Violates the strict no-ignored-errors policy. All errors must be handled, safely reported, or logged via centralized functions.
**- Files Affected:** `**/*.go`, `**/*.js`, `**/*.ts`

## IGNORE: Centralized SDK Configuration

**- Pattern:** Deleting `mise.toml` files in SDK subdirectories (`sdk/*/mise.toml`) and moving their configuration tasks to the root `mise.toml`.
**- Justification:** SDKs must remain self-contained with their own tooling configuration to maintain modularity and avoid complicating the root project configuration.
**- Files Affected:** `mise.toml`, `sdk/**/mise.toml`

## IGNORE: Relaxing Markdown Lint Rules

**- Pattern:** Creating or modifying configuration files (e.g., `.markdownlint-cli2.yaml`) to relax default markdown linting rules, such as increasing line length limits or disabling rules.
**- Justification:** The project enforces default `markdownlint-cli2` rules. Code must be fixed to comply with the rules rather than relaxing the rules themselves.
**- Files Affected:** `.markdownlint-cli2.yaml`, `**/*.md`

## IGNORE: Unsafe Test Error Reporting in Goroutines

**- Pattern:** Calling `t.Fatalf` or `panic` inside mock server handlers or other functions that run in separate goroutines during tests (e.g., inside `httptest.NewServer`).
**- Justification:** Calling `t.Fatalf` from a separate goroutine is unsafe, triggers `go vet` violations (`testinggoroutine`), and can crash the test runner. `t.Errorf` must be used instead.
**- Files Affected:** `**/*_test.go`
