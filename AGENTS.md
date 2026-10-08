# once-campfire-go-gina

Port of [basecamp/once-campfire-go](https://github.com/basecamp/once-campfire-go) whose HTTP and
WebSocket layers run on [gina](https://github.com/rm4n0s/gina) instead of `net/http` and
`coder/websocket`. Behaviour above the transport must match the reference (`reference/`, a pinned
submodule; the Rails source is `reference/reference/`). Do not edit `reference/`.

- Handlers are written against `internal/httpx` (not `net/http`). `internal/front` is the only
  place that talks to gina's HTTP servers; `internal/cable` is the only place that talks to gina's
  WebSocket server; `internal/push` is the only place that talks to gina's Web Push sender.
  `net/http` belongs to outbound clients (`internal/integrations`) and tests.
- gina runs handlers synchronously on shard threads and shares one heap: state shared across
  requests (the web `Server`, the cable `Hub`) must be safe for concurrent use. Never block a
  handler on something slow if it can go through `internal/jobs`.
- No goroutines in non-test code: anything that runs concurrently is an isolate on a shard (background work
  is a job on `internal/jobs`, which runs on job shards). Blocking work belongs on shards that run no HTTP.
- Tests live beside code and use real sockets (`internal/front/fronttest`) when the transport
  matters, `httpxtest` recorders when it does not.
- Run `gofmt`, `go vet -tags sqlite_fts5 ./...` and `go test -race -tags sqlite_fts5 ./...`
  (`bin/check`). SQLite needs `CGO_ENABLED=1` and `-tags sqlite_fts5`.
- Record intentional differences from the reference in README.md.
