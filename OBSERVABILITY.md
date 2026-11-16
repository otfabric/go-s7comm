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
  upload cleanup, …), on the goroutine of that call, while it holds the
  client's turn. Keep implementations non-blocking. A callback may call
  `ConnectionInfo`, `PDUSize` and `Close` of the client (`Close` ends the call
  that logs); an operation or `Connect` of the same client called from a
  callback waits for that call and returns only when its own context ends. A
  callback that does not return normally is not recovered by the client: the
  unwinding reaches the caller of the call.
- **Adapter** — `client.NewSlogLogger(handler)` wraps any `slog.Handler`.
  `NewSlogLogger(nil)` is a no-op logger.

```go
logger := client.NewSlogLogger(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
c := client.New("10.0.0.1", client.WithLogger(logger))
```

See [API.md](API.md) (Client options) for `WithLogger` / `NewSlogLogger`.

## Server logging

`server.Config.Logger` takes a `*slog.Logger`; nil discards everything. Attributes are low-cardinality (`remote`, `pduRef`, `function`); memory contents are never logged.

| Level | Event |
|---|---|
| Info | listening; connection open; connection closed (with the reason) |
| Warn | connection refused at `MaxConnections`; connection dropped on a PDU whose header cannot be read |
| Debug | failed COTP handshake; setup communication (requested and granted PDU); job or user data not implemented (answered `0x8104`); malformed job |

## Context and cancellation

Cancel and deadline behaviour (a call given up after its request was sent
ends the session), the request timeout (`WithRequestTimeout`) and `Close`
ending the call in flight are documented in
[ERRORS.md](ERRORS.md#context-reconnect-and-concurrency) and the README. The
client logs nothing when a session ends this way, when a request runs out of
time or when `Close` ends an operation: the call's own error says so. A
`Connect` that fails is logged at Error as before, also one that `Close`
ended (its error matches `ErrClosed`).
