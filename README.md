# once-campfire-go-gina

[ONCE Campfire](https://github.com/basecamp/once-campfire-go) in Go, with the HTTP and WebSocket
layers moved from `net/http` and `coder/websocket` to [gina](https://github.com/rm4n0s/gina): its
HTTP/1.1 and **HTTP/2** servers, its TLS 1.3 server and its **WebSocket** server. Everything above
that layer — the Rails-compatible SQLite schema, cookies and signed streams, the Turbo/Stimulus
frontend, rich text, search, storage, bots, webhooks, Web Push — is the upstream code, MIT licensed
(see [LICENSE](LICENSE) and [NOTICE](NOTICE)). The application behaves as the reference does; only how bytes get
in and out changed.

```
browser ──h2/h1.1 (TLS 1.3) ──┐                      ┌── shard 0 ── listener + one isolate per connection ─┐
browser ──wss over h2/h1.1────┤  SO_REUSEPORT        ├── shard 1 ── …                                      ├─ internal/web
curl    ──http/1.1 (plain)────┘                      └── shard N-1                                         ┘  (handlers run here)
                                                      shard N   ── cable bus isolate: fan-out, re-authorisation, pings
```

## What replaced what

| Upstream | Here |
|---|---|
| `net/http` server, `http.ResponseWriter`/`*http.Request` in every handler | [`internal/httpx`](internal/httpx): the same request/response model (headers, cookies, forms, `ServeContent` with ranges, …) with no socket behind it. [`internal/front`](internal/front) fills it in from gina's HTTP/1.1 or HTTP/2 exchange and writes the result back. |
| Thruster-like front server on `net/http` (HTTP/2, TLS, autocert, gzip/zstd, response cache) | [`internal/front`](internal/front): gina `http` + `http2` servers (one port speaks h2 **and** HTTP/1.1 via ALPN), gina's TLS 1.3, `autocert` over TLS-ALPN-01 with hot certificate swap, gzip with a memoised cache, X-Forwarded-* handling, HTTP→HTTPS redirect. |
| `github.com/coder/websocket` + a patched copy in `third_party/` | [`internal/cable`](internal/cable) on gina's `websocket` extension: Action Cable over ws/wss on HTTP/1.1 **and** RFC 8441 (WebSocket over HTTP/2). Broadcasts build one immutable buffer and push it to every subscriber (`PushShared`). |
| libvips (cgo) | Pure Go: `image/*`, `x/image` (WebP/TIFF decode), `nativewebp` (WebP encode), EXIF orientation. |
| libzstd (cgo) | Dropped; gzip only. |
| Go 1.27's `uuid` | [`internal/uuid`](internal/uuid) (Go 1.26 builds). |

`net/http` is still imported in exactly one place on purpose: [`internal/integrations`](internal/integrations)
(and test helpers). Those are *outbound* clients — link-preview fetching, webhooks, Web Push delivery —
and gina is a server library. No `net/http` server and no third-party WebSocket library remain.

## Build and run

Requirements: Go ≥ 1.26, a C compiler (SQLite is cgo), Python 3 (asset build), and optionally
`ffmpeg`/`ffprobe` for video and audio metadata and poster frames. No libvips, no libzstd.

```sh
git submodule update --init --recursive      # the pinned Rust + Rails sources the frontend is built from
bin/build                                    # builds the assets, then ./campfire
export SECRET_KEY_BASE="$(openssl rand -hex 64)"
DISABLE_SSL=1 HTTP_PORT=8080 ./campfire server
```

Open <http://localhost:8080/first_run>. Keep the same `SECRET_KEY_BASE` across restarts. The
storage layout (`CAMPFIRE_STORAGE_PATH`, `db/`, `files/`, `backups/`, `thruster/`), `campfire backup`
and `campfire db:prepare` are as upstream.

```sh
docker build -t campfire-gina .
docker run --rm -p 8080:80 -e DISABLE_SSL=1 -e SECRET_KEY_BASE -v campfire-storage:/rails/storage campfire-gina
```

### Listeners and TLS

| Setting | Effect |
|---|---|
| `HTTP_PORT` (80) | Plain HTTP/1.1. With TLS configured it only redirects to HTTPS (301). `H2C_ENABLED=1` also accepts cleartext HTTP/2 (the connection preface decides). |
| `HTTPS_PORT` (443) | Used when TLS is configured. **HTTP/2 and HTTP/1.1 on one port** (ALPN), WebSocket over either. |
| `TLS_DOMAIN=a.example,b.example` | Certificates from Let's Encrypt (`ACME_DIRECTORY`, `EAB_KID`/`EAB_HMAC_KEY`), cached in `STORAGE_PATH`. Validation is TLS-ALPN-01 on `HTTPS_PORT`, answered by the server itself. Until the first certificate arrives, handshakes get a throwaway self-signed one. Renewal runs in the background and swaps certificates without a restart. |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | A static certificate instead (prefer ECDSA or Ed25519: handshakes run on the shard thread). |
| `TARGET_BIND`:`TARGET_PORT` (127.0.0.1:3000) | The internal application listener, plain HTTP/1.1, no forwarded-header injection — what Thruster proxied to. |
| `SHARDS` (min(CPUs, 4)) | Shard threads that run HTTP. One more hosts the Action Cable bus. `PIN_SHARDS=1` pins them to cores. |
| `GZIP_COMPRESSION_ENABLED`, `GZIP_COMPRESSION_LEVEL` (5), `GZIP_CACHE_SIZE` (32 MiB) | gzip of text responses; compressed bodies are memoised by the SHA-256 of their bytes. |
| `HTTP_IDLE_TIMEOUT`, `HTTP_READ_TIMEOUT`, `HTTP_WRITE_TIMEOUT`, `MAX_REQUEST_BODY`, `CAMPFIRE_MAX_UPLOAD_BYTES` | Limits (uploads default to 10 GiB, as upstream). |
| `FORWARD_HEADERS`, `LOG_REQUESTS` | As Thruster. |

Public listeners are dual-stack (`::`) when the host has IPv6.

## Tests

```sh
bin/check                                    # gofmt, assets, vet, every test under -race
go test -tags sqlite_fts5 -race ./...        # the same without the asset step
```

The upstream package tests (Rails cookie/stream vectors, 658 rich-text cases, user agents, QR codes,
storage, database, access control, Open Graph, webhooks, Web Push) are kept and run against the pinned
`reference/` vectors. The `web` suite now talks to a **real gina server** over loopback. New:

- [`internal/web/e2e_test.go`](internal/web/e2e_test.go): Action Cable over ws (HTTP/1.1), wss (HTTP/1.1) and wss over HTTP/2 — welcome, subscribe/reject, message broadcast to several sockets, typing, presence, session-end disconnect, the 3-second ping — and a 40 MiB multipart upload with ranged download over HTTP/1.1 and HTTP/2.
- [`internal/front`](internal/front): request/response model on both protocols, header-injection and panic containment, gzip, uploads from 0 bytes to 33 MiB (including unknown length), ranges and HEAD, HTTP→HTTPS redirect, HTTP/1.1 fallback on the TLS port, concurrency over three shards.
- [`internal/front/acme_test.go`](internal/front/acme_test.go): a real certificate from a local [Pebble](https://github.com/letsencrypt/pebble) CA through TLS-ALPN-01 (skipped unless `pebble` is on `PATH` or `PEBBLE_BIN` is set).
- [`internal/httpx`](internal/httpx): headers, cookies, forms, `MaxBytesReader`, `ServeContent`.

## Differences from upstream — read before replacing an installation

- **Handlers run synchronously on a shard thread.** A slow call (a bcrypt login, generating an image variant, a cold disk read) stalls the other connections on that shard for its duration. Slow background work already goes through the job queue; raise `SHARDS` if logins are bursty.
- **TLS is gina's own TLS 1.3 server**: no TLS 1.2, no session resumption, no client certificates, not security-audited (see gina's README). Clients that cannot speak TLS 1.3 cannot connect over HTTPS; put a TLS-terminating proxy in front and use `TARGET_PORT` if you need them.
- **Request bodies arrive before the handler runs.** Up to 1 MiB stays in memory; larger or unknown-length bodies are spooled to `<storage>/tmp` and parsed from disk, so memory stays flat for big uploads — but an unauthenticated 5 GiB upload is received before it is refused (upstream's multipart parser streamed to disk before auth too).
- **No `100 Continue`.** gina's HTTP/1.1 server does not answer `Expect: 100-continue`; `curl` therefore waits one second before sending bodies over 1 MiB. Browsers are unaffected.
- **No WebSocket compression.** `permessage-deflate` is not negotiated (gina serves clients without it); broadcasts are sent uncompressed from a shared buffer. A client that falls too far behind is closed with 1013 and reconnects.
- **Media.** Images are processed in pure Go: AVIF, HEIC and SVG are not decoded (no dimensions or variants), variants are not byte-identical to libvips output, and WebP variants are lossless (larger than libvips' lossy WebP). Video/audio still need `ffmpeg`/`ffprobe`.
- **Compression and caching.** zstd, Thruster's response cache (`CACHE_SIZE`, `MAX_CACHE_ITEM_SIZE`) and `GZIP_COMPRESSION_JITTER` are gone; the application's own fragment cache is unchanged.
- **Not run in a browser.** The frontend assets are upstream's, byte for byte, and the protocol layers are exercised with Go clients (including `x/net/http2` extended CONNECT) and curl, but the upstream Playwright/screenshot/parity tooling (`check-browser`, `check-screens`, `check-upgrade`, `check-container`) was not ported and has not been run against this build. Neither gina's HTTP/2 nor its WebSocket server has been through h2spec or Autobahn.
- Only HTTP-visible behaviour of the realtime layer changed internally: authorisation is still re-checked for every publication, now on the bus isolate.

## Layout

```
cmd/campfire        entry point (server, db:prepare, backup)
assets/             embeds the built Turbo/Stimulus frontend
internal/httpx      request/response model (+ httpxtest recorder)
internal/front      gina listeners, TLS, ACME, gzip, request/response glue (+ fronttest: server and WebSocket client for tests)
internal/cable      Action Cable on gina's WebSocket server
internal/web        routes, handlers, templates
internal/database   SQLite access, schema, search          internal/storage   blobs, image/video processing
internal/rails      cookies, signed streams, verifiers     internal/richtext  sanitising, autolinking
internal/integrations  outbound HTTP: unfurl, webhooks, Web Push
reference/          pinned basecamp/once-campfire-rust (+ Rails source): frontend sources, schema, test vectors
```

## License

MIT; see [LICENSE](LICENSE) and [NOTICE](NOTICE). Third-party notices for copied algorithms are in
[licenses](licenses/) and [internal/html/LICENSE](internal/html/LICENSE); dependencies keep their own licenses.
