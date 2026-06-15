FROM jdxcode/mise:latest AS builder

WORKDIR /app
COPY go.mod go.sum mise.toml ./
RUN mise trust && mise install
RUN mise run install
COPY . .
# Disable CGO for static build compatible with Alpine
RUN mise exec -- env CGO_ENABLED=0 go build -o fetchurl ./cmd/fetchurl

FROM alpine:3.21
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/fetchurl /app/fetchurl
ENTRYPOINT ["/app/fetchurl"]
