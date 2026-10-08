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
| Web Push: own aes128gcm encryption, VAPID signing and a `net/http` POST | [`internal/push`](internal/push) on gina's [`extensions/webpush`](https://github.com/rm4n0s/gina): encryption (RFC 8291), VAPID (RFC 8292), retries/backoff and the connect-time SSRF check come from gina; the app keeps its endpoint policy and a blocking `Send` for its jobs. |
| libvips (cgo) | Pure Go: `image/*`, `x/image` (WebP/TIFF decode), `nativewebp` (WebP encode), EXIF orientation. |
| libzstd (cgo) | Dropped; gzip only. |
| Go 1.27's `uuid` | [`internal/uuid`](internal/uuid) (Go 1.26 builds). |

No `net/http` server and no third-party WebSocket library remain. `net/http` is still used for
*outbound* requests, which gina (a server library) does not make: link-preview fetching and webhooks in
[`internal/integrations`](internal/integrations), and the POST to the push service inside gina's own
`extensions/webpush` transport (its README says so). Test helpers use it as a client.

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
| `TLS_DOMAIN=a.example,b.example` | Certificates from Let's Encrypt (`ACME_DIRECTORY`, `EAB_KID`/`EAB_HMAC_KEY`), cached in `STORAGE_PATH`. Validation is TLS-ALPN-01 on `HTTPS_PORT`, answered by the server itself. Until the first certificate arrives, handshakes get a throwaway self-signed one. Renewal runs as an isolate on a shard of its own and swaps certificates without a restart. |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | A static certificate instead (prefer ECDSA or Ed25519: handshakes run on the shard thread). |
| `TARGET_BIND`:`TARGET_PORT` (127.0.0.1:3000) | The internal application listener, plain HTTP/1.1, no forwarded-header injection — what Thruster proxied to. |
| `SHARDS` (min(CPUs, 4)) | Shard threads that run HTTP. One more hosts the Action Cable bus (and Web Push). `PIN_SHARDS=1` pins them to cores. |
| `JOB_CONCURRENCY` (2) | Job shards: threads that run background jobs (push, webhooks, purge, ban, analyze) and nothing else. Each job runs to completion on its shard, so this is also how many slow jobs can be in flight. With TLS via ACME, one more shard runs certificate renewal. |
| `GZIP_COMPRESSION_ENABLED`, `GZIP_COMPRESSION_LEVEL` (5), `GZIP_CACHE_SIZE` (32 MiB) | gzip of text responses; compressed bodies are memoised by the SHA-256 of their bytes. |
| `HTTP_IDLE_TIMEOUT`, `HTTP_READ_TIMEOUT`, `HTTP_WRITE_TIMEOUT`, `MAX_REQUEST_BODY`, `CAMPFIRE_MAX_UPLOAD_BYTES` | Limits (uploads default to 10 GiB, as upstream). |
| `FORWARD_HEADERS`, `LOG_REQUESTS` | As Thruster. |

Public listeners are dual-stack (`::`) when the host has IPv6.

## Tests

```sh
bin/check                                    # gofmt, assets, vet, every test under -race
GOEXPERIMENT=jsonv2 go test -tags sqlite_fts5 -race ./...   # the same without the asset step
```

The upstream package tests (Rails cookie/stream vectors, 658 rich-text cases, user agents, QR codes,
storage, database, access control, Open Graph, webhooks, Web Push) are kept and run against the pinned
`reference/` vectors. The `web` suite now talks to a **real gina server** over loopback. New:

- [`internal/web/e2e_test.go`](internal/web/e2e_test.go): Action Cable over ws (HTTP/1.1), wss (HTTP/1.1) and wss over HTTP/2 — welcome, subscribe/reject, message broadcast to several sockets, typing, presence, session-end disconnect, the 3-second ping — and a 40 MiB multipart upload with ranged download over HTTP/1.1 and HTTP/2.
- [`internal/front`](internal/front): request/response model on both protocols, header-injection and panic containment, gzip, uploads from 0 bytes to 33 MiB (including unknown length), ranges and HEAD, HTTP→HTTPS redirect, HTTP/1.1 fallback on the TLS port, concurrency over three shards.
- [`internal/front/acme_test.go`](internal/front/acme_test.go): a real certificate from a local [Pebble](https://github.com/letsencrypt/pebble) CA through TLS-ALPN-01 (skipped unless `pebble` is on `PATH` or `PEBBLE_BIN` is set).
- [`internal/httpx`](internal/httpx): headers, cookies, forms, `MaxBytesReader`, `ServeContent`.
- [`internal/push`](internal/push) and [`internal/web/push_delivery_test.go`](internal/web/push_delivery_test.go): gina's encryptor checked by an independent RFC 8291 decryptor that is itself validated on the Rust reference ciphertext; deliveries through a running gina system to a fake push service (headers, VAPID, retries, 404/410/4xx/5xx mapping, endpoint policy, cancellation, 80 concurrent sends); a message in a room reaching another member's subscription end to end, and a `410` deleting the subscription.

## Performance

`bench/run` is the upstream project's benchmark harness (`reference/bench/run`) pointed at this
image: same Rails-generated seed, same load generator, same suites, same CPU budget. Each app runs
alone in a container pinned to four hardware threads (`--cpuset-cpus 8-11`, host networking), the load
generator on four others. Medians of three runs, AMD Ryzen AI MAX+ 395. The Rails column is from the
interleaved Rails/gina run of 7 October 2026 ([`bench/results/gina-20261007`](bench/results/gina-20261007/report.md),
which also holds the gina figures of that day). The Go column is a gina-only rerun of 8 October 2026
([`bench/results/gina-jobs-isolates-20261008`](bench/results/gina-jobs-isolates-20261008/report.md)) with
background jobs and ACME renewal on shards instead of goroutines, on a tree that also carried the
uncommitted `WriteBody`/recorded-parts work in `internal/front/exchange.go`; it was not benchmarked
without either change, so the difference from the 7 October gina figures cannot be assigned to one of them.
Per-rep JSON is in each directory.

```sh
parity/bin/reference build                    # in reference/: Rails image
PARITY_SEED_DIR=$PWD/.bench/seed reference/parity/bin/seed build default
docker build -t campfire-gina:app .
bench/run --apps reference,gina --reps 3
```

The **Rust** column is not from this run: it is the figure published in
[basecamp/once-campfire-rust](https://github.com/basecamp/once-campfire-rust) (1 October 2026, same
CPU model, same suites). Its Rails numbers agree with ours within about 10% on the HTTP routes (uploads differ more: 107 ms
there), so the columns are comparable, but they are not the same run.

| Measurement | Rails | Go (gina) | Rust (published) |
|---|---|---|---|
| Room page, 16 clients (req/s) | 203 | 45,529 | 36,120 |
| Messages page, 16 clients (req/s) | 396 | 43,209 | 41,352 |
| Sidebar, 16 clients (req/s) | 517 | 22,372 | 34,339 |
| Search, 16 clients (req/s) | 355 | 39,733 | 33,510 |
| `/up`, 16 clients (req/s) | 3,671 | 231,058 | 233,085 |
| Avatar, 16 clients (req/s) | 93,531 | 43,103 | 389,632 |
| Static CSS, 16 clients (req/s) | 124,436 | 335,288 | 406,724 |
| Room page p99, 64 clients (ms) | 484 | 4.4 | 3.1 |
| Post a message, 16 clients (req/s) | 266 | 3,833 [3,499–4,084] | 6,817 |
| Post a message p99, 64 clients (ms) | 377 | 34 [32–265] | 13.9 |
| Upload a 505 KB JPEG until its thumbnail is served (ms) | 78 | 258 | 27.6 |
| Cold start until `/up` answers (ms) | 2,845 | 144 [133–228] | 157 |
| Idle memory, container (MB) | 300 | 37 | 20 |
| Peak anon memory under load (MB) | 1,439 | 1,675 | 381 |
| Connect and subscribe 1,000 cable clients (s) | 1.76 | 0.18 | 0.13 |
| Post to all 1,000 clients received, paced, p50 (ms) | 83 | 8.0 | 6.5 |
| Saturated post to all 1,000 clients received, p50 (ms) | 293 | 15,032 [433–15,114] | 15.7 |

Bracketed values are the min–max across the three reps. The suite ran with 100, 500 and 1,000 cable
clients; the 5,000 and 10,000 client scenarios of the upstream headline table were not run.

What the numbers say:

- **Reads and cold paths are the strength.** Pages, search and the sidebar run 43–224× faster than
  Rails with a 110× lower room-page p99, and the process starts 20× faster and idles at an eighth of
  the memory. Against the published Rust figures (a different run) room, messages and search are level
  or ahead and the sidebar is 1.5× behind.
- **Posting a message is faster than before but not steady.** At 16 clients the three reps measured
  3,499–4,084 req/s (p99 10 ms), 14× Rails' ~265 and 1.8× behind Rust. At 1 and 64 clients one rep
  fell to 478 and 470 req/s, so the low outliers of the 7 October run (53 req/s at 16 clients) have
  not gone away entirely.
- **Saturated fan-out is still the weak point.** Under a flood of posts the POST itself returns in
  under a millisecond, but at 1,000 clients a message reaches every client only after ~15 s (median;
  one rep 0.4 s); at 100 clients the median is 13 s [0.04–15 s]; at 500 clients it was 124 ms. The
  realtime bus does not keep up with the posting rate when the backlog builds, and peak memory
  (1.7 GB) ends above Rails' 1.4 GB instead of Rust's 0.4 GB. Paced delivery, one post at a time, is
  fast: 10× lower latency than Rails at 1,000 clients.
- **Idle memory rose from 27 to 37 MB.** This run set `JOB_CONCURRENCY=3`, so jobs now have three
  shards of their own, which most likely accounts for it (each shard reserves its message slots; not
  measured separately). Cold start is unchanged within the spread.
- **Uploads are 3.3× slower than Rails** (258 ms against 78 ms), the cost of processing images in
  pure Go instead of libvips.
- **Avatars run at under half of Rails' speed** (43k against 94k req/s).

These are measurements of the current tree, taken before any tuning; the saturated fan-out, upload
and avatar items are open work, not intended differences.

## Differences from upstream — read before replacing an installation

- **Handlers run synchronously on a shard thread.** A slow call (a bcrypt login, generating an image variant, a cold disk read) stalls the other connections on that shard for its duration. Slow background work already goes through the job queue, which runs on job shards of its own; raise `SHARDS` if logins are bursty.
- **TLS is gina's own TLS 1.3 server**: no TLS 1.2, no session resumption, no client certificates, not security-audited (see gina's README). Clients that cannot speak TLS 1.3 cannot connect over HTTPS; put a TLS-terminating proxy in front and use `TARGET_PORT` if you need them.
- **Request bodies arrive before the handler runs.** Up to 1 MiB stays in memory; larger or unknown-length bodies are spooled to `<storage>/tmp` and parsed from disk, so memory stays flat for big uploads — but an unauthenticated 5 GiB upload is received before it is refused (upstream's multipart parser streamed to disk before auth too).
- **No goroutines of our own.** Everything the application runs itself is an isolate on a shard: HTTP and WebSocket connections, the Action Cable bus, Web Push, ACME renewal, and background jobs (`internal/jobs`, on `JOB_CONCURRENCY` job shards instead of a goroutine pool). What remains is not ours: gina's thread host, `net/http` clients for webhooks and unfurling, `database/sql`, and the signal goroutine in the standard library. Consequences against the reference's per-queue worker pools: a job runs to completion on its shard, so the five kinds share `JOB_CONCURRENCY` shards and one slow webhook can hold up a push notification queued behind it on the same shard; the 1024-job cap is per kind and a job that cannot be delivered to a shard is dropped and logged; on shutdown the queues drain (up to 10 s) while the listeners still accept requests, instead of after they stop; and a renewal stuck on a hung ACME server holds up shutdown (gina has no watchdog to interrupt a handler).
- **JSON is `encoding/json/v2`, which Go 1.26 still gates behind `GOEXPERIMENT=jsonv2`.** `bin/build`, `bin/check`, the Dockerfile and `mise.toml` set it; set it yourself for plain `go build`/`go test`/`gopls`. `internal/jsonx` restores v1's output where the reference depends on it (`<>&` and U+2028/9 escaped, sorted keys, nil slices as `null`) and keeps number text exact (`1080.0` stays `1080.0`). v2 is stricter on input than v1 was: field names match case-sensitively, invalid UTF-8 and duplicate keys are errors (except in Trix attributes and parsed parameters, which accept duplicates like Ruby), and a request body with trailing data after the JSON value is rejected.
- **No `100 Continue`.** gina's HTTP/1.1 server does not answer `Expect: 100-continue`; `curl` therefore waits one second before sending bodies over 1 MiB. Browsers are unaffected.
- **No WebSocket compression.** `permessage-deflate` is not negotiated (gina serves clients without it); broadcasts are sent uncompressed from a shared buffer. A client that falls too far behind is closed with 1013 and reconnects.
- **Media.** Images are processed in pure Go: AVIF, HEIC and SVG are not decoded (no dimensions or variants), variants are not byte-identical to libvips output, and WebP variants are lossless (larger than libvips' lossy WebP). Video/audio still need `ffmpeg`/`ffprobe`.
- **Compression and caching.** zstd, Thruster's response cache (`CACHE_SIZE`, `MAX_CACHE_ITEM_SIZE`) and `GZIP_COMPRESSION_JITTER` are gone; the application's own fragment cache is unchanged.
- **Not run in a browser.** The frontend assets are upstream's, byte for byte, and the protocol layers are exercised with Go clients (including `x/net/http2` extended CONNECT) and curl, but the upstream Playwright/screenshot/parity tooling (`check-browser`, `check-screens`, `check-upgrade`, `check-container`) was not ported and has not been run against this build. Neither gina's HTTP/2 nor its WebSocket server has been through h2spec or Autobahn.
- **Web Push** keeps the reference's endpoint policy (HTTPS, port 443, the known push services), TTL (28 days), urgency and VAPID environment (`VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, `VAPID_SUBJECT`), but runs on gina's sender: delivery is best effort and at most once (work still queued at shutdown is dropped), retries are gina's (two retries on 429/5xx/network errors, honouring `Retry-After`), subscription keys must be a 65-byte uncompressed P-256 point and a 16-byte auth secret (browsers always send those; the reference also tolerated compressed points), and the "send test notification" button holds its shard thread for up to 15 s if the push service hangs. gina's sender has been run only against fake push services, never a real browser or FCM/Mozilla/Apple.
- Only HTTP-visible behaviour of the realtime layer changed internally: authorisation is still re-checked for every publication, now on the bus isolate.

## Layout

```
cmd/campfire        entry point (server, db:prepare, backup)
assets/             embeds the built Turbo/Stimulus frontend
internal/httpx      request/response model (+ httpxtest recorder)
internal/front      gina listeners, TLS, ACME, gzip, request/response glue (+ fronttest: server and WebSocket client for tests)
internal/cable      Action Cable on gina's WebSocket server
internal/jobs       background queues: worker isolates on job shards
internal/push       Web Push on gina's webpush extension (blocking Send for the job queue)
internal/web        routes, handlers, templates
internal/database   SQLite access, schema, search          internal/storage   blobs, image/video processing
internal/rails      cookies, signed streams, verifiers     internal/richtext  sanitising, autolinking
internal/integrations  outbound HTTP: unfurl, webhooks; Web Push endpoint policy
reference/          pinned basecamp/once-campfire-rust (+ Rails source): frontend sources, schema, test vectors
```

## License

MIT; see [LICENSE](LICENSE) and [NOTICE](NOTICE). Third-party notices for copied algorithms are in
[licenses](licenses/) and [internal/html/LICENSE](internal/html/LICENSE); dependencies keep their own licenses.
