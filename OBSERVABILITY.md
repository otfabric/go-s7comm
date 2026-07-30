# Observability: otfabric/go-s7comm

Logging for the S7 client. There is **no** metrics callback surface today;
absence is intentional unless a concrete consumer needs one.

## Logging (silent by default)

`WithLogger` is optional. When unset (nil), the client does not log.

```go
type Logger interface {
    Debug(msg string, args ...interface{})
    Info(msg string, args ...interface{})
    Error(msg string, args ...interface{})
}
```

- **Style** — printf-style: `msg` is a format string; `args` are for `fmt.Sprintf`
  (e.g. `Error("Connect failed: %v", err)`).
- **Cost** — callbacks run **synchronously** on the request path (Connect,
  upload cleanup, …). Keep implementations non-blocking.
- **Adapter** — `client.NewSlogLogger(handler)` wraps any `slog.Handler`.
  `NewSlogLogger(nil)` is a no-op logger.

```go
logger := client.NewSlogLogger(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
c := client.NewClient("10.0.0.1", client.WithLogger(logger))
```

See [API.md](API.md) (Client options) for `WithLogger` / `NewSlogLogger`.

## Context and cancellation

Cancel and deadline behaviour (including session abort on mid-I/O cancel) is
documented in [ERRORS.md](ERRORS.md#context-reconnect-and-concurrency) and the
README.
