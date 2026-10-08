```
date: 2026-10-08T06:11:57+03:00
host: 7.2.9-300.vanilla.fc44.x86_64, AMD RYZEN AI MAX+ 395 w/ Radeon 8060S, 32 threads, 30GB
server cpus: 8-11 (nproc 4); loadgen cpus: 12-15; network: host
env: WEB_CONCURRENCY=3 JOB_CONCURRENCY=3 RAILS_MAX_THREADS=5 
gina extra env: 
user agent: (none)
gina image: campfire-gina:app sha256:e79ee6e977494c25404605690a38fe68c052eff8b1e6a0b8fb2924a7d0ed63ce 2026-10-08T06:11:52.552576874+03:00
gina HEAD: 435de36 (dirty: 7 files)
```

Reps: gina 3. Cells: median [min–max].

### Startup and memory

| Metric | Go (gina) | Go adv. |
|---|---|---|
| cold start: docker run → /up 200 (ms) | 144 [133–228] | – |
| idle memory.current (MB) | 37.0 [37.0–40.0] | – |
| idle anon (MB) | 35.0 [35.0–35.0] | – |
| peak memory.current under load (MB) | 1,798 [1,752–2,136] | – |
| peak anon under load (MB) | 1,675 [1,565–2,032] | – |

### HTTP (signed in as david; keep-alive; c = concurrent connections)

| Metric | Go (gina) | Go adv. |
|---|---|---|
| room_show c=1 req/s | 10,343 [10,205–10,349] | – |
| room_show c=1 p50 ms | 0.09 [0.09–0.10] | – |
| room_show c=1 p99 ms | 0.12 [0.11–0.12] | – |
| room_show c=16 req/s | 45,529 [43,500–45,901] | – |
| room_show c=16 p50 ms | 0.30 [0.24–0.37] | – |
| room_show c=16 p99 ms | 0.90 [0.72–1.09] | – |
| room_show c=64 req/s | 44,884 [44,834–45,059] | – |
| room_show c=64 p50 ms | 1.29 [1.20–1.40] | – |
| room_show c=64 p99 ms | 4.41 [4.29–4.44] | – |
| messages_page c=1 req/s | 9,853 [9,690–10,084] | – |
| messages_page c=1 p50 ms | 0.10 [0.10–0.10] | – |
| messages_page c=1 p99 ms | 0.12 [0.11–0.13] | – |
| messages_page c=16 req/s | 43,209 [41,662–43,235] | – |
| messages_page c=16 p50 ms | 0.34 [0.31–0.47] | – |
| messages_page c=16 p99 ms | 0.92 [0.88–1.02] | – |
| messages_page c=64 req/s | 43,282 [43,085–43,660] | – |
| messages_page c=64 p50 ms | 1.38 [1.37–1.44] | – |
| messages_page c=64 p99 ms | 4.52 [4.39–4.60] | – |
| sidebar c=1 req/s | 5,847 [5,637–6,493] | – |
| sidebar c=1 p50 ms | 0.17 [0.15–0.17] | – |
| sidebar c=1 p99 ms | 0.20 [0.18–0.20] | – |
| sidebar c=16 req/s | 22,372 [22,140–23,623] | – |
| sidebar c=16 p50 ms | 0.61 [0.59–0.66] | – |
| sidebar c=16 p99 ms | 1.55 [1.51–1.61] | – |
| sidebar c=64 req/s | 22,131 [21,986–23,628] | – |
| sidebar c=64 p50 ms | 2.85 [2.77–2.91] | – |
| sidebar c=64 p99 ms | 6.38 [6.10–6.39] | – |
| search c=1 req/s | 10,060 [9,795–10,585] | – |
| search c=1 p50 ms | 0.10 [0.09–0.10] | – |
| search c=1 p99 ms | 0.12 [0.11–0.12] | – |
| search c=16 req/s | 39,733 [38,724–40,024] | – |
| search c=16 p50 ms | 0.43 [0.42–0.43] | – |
| search c=16 p99 ms | 0.85 [0.76–0.90] | – |
| search c=64 req/s | 39,876 [39,380–40,250] | – |
| search c=64 p50 ms | 1.54 [1.34–1.69] | – |
| search c=64 p99 ms | 3.53 [3.47–3.62] | – |
| avatar c=1 req/s | 10,104 [9,921–10,126] | – |
| avatar c=1 p50 ms | 0.10 [0.10–0.10] | – |
| avatar c=1 p99 ms | 0.12 [0.12–0.12] | – |
| avatar c=16 req/s | 43,103 [43,043–43,876] | – |
| avatar c=16 p50 ms | 0.38 [0.35–0.39] | – |
| avatar c=16 p99 ms | 0.77 [0.71–0.80] | – |
| avatar c=64 req/s | 43,353 [43,078–43,423] | – |
| avatar c=64 p50 ms | 1.31 [1.29–1.45] | – |
| avatar c=64 p99 ms | 4.05 [3.99–4.13] | – |
| static_css c=1 req/s | 33,763 [33,506–33,959] | – |
| static_css c=1 p50 ms | 0.03 [0.03–0.03] | – |
| static_css c=1 p99 ms | 0.04 [0.04–0.04] | – |
| static_css c=16 req/s | 335,288 [292,960–346,938] | – |
| static_css c=16 p50 ms | 0.04 [0.03–0.05] | – |
| static_css c=16 p99 ms | 0.10 [0.10–0.10] | – |
| static_css c=64 req/s | 359,292 [358,367–364,895] | – |
| static_css c=64 p50 ms | 0.13 [0.13–0.15] | – |
| static_css c=64 p99 ms | 1.13 [0.98–1.21] | – |
| up c=1 req/s | 29,183 [28,909–29,309] | – |
| up c=1 p50 ms | 0.03 [0.03–0.03] | – |
| up c=1 p99 ms | 0.04 [0.04–0.04] | – |
| up c=16 req/s | 231,058 [223,710–232,466] | – |
| up c=16 p50 ms | 0.05 [0.05–0.06] | – |
| up c=16 p99 ms | 0.13 [0.12–0.15] | – |
| up c=64 req/s | 230,086 [229,870–232,298] | – |
| up c=64 p50 ms | 0.23 [0.23–0.26] | – |
| up c=64 p99 ms | 2.46 [2.41–2.69] | – |
| post_message c=1 req/s | 2,520 [478–2,539] | – |
| post_message c=1 p50 ms | 0.33 [0.32–0.43] | – |
| post_message c=1 p99 ms | 2.79 [2.77–77.76] | – |
| post_message c=16 req/s | 3,833 [3,499–4,084] | – |
| post_message c=16 p50 ms | 3.29 [2.71–3.82] | – |
| post_message c=16 p99 ms | 10.6 [10.1–13.9] | – |
| post_message c=64 req/s | 3,805 [470–3,805] | – |
| post_message c=64 p50 ms | 18.1 [15.4–121.6] | – |
| post_message c=64 p99 ms | 33.7 [31.8–264.7] | – |

### HTTP errors / non-2xx-3xx (first rep, per app)

| Metric | Go (gina) | Go adv. |
|---|---|---|
- gina: none

### Action Cable fan-out (one room; chatter.js subscriptions per client)

| Metric | Go (gina) | Go adv. |
|---|---|---|
| 100 clients: subscribed | 100 [100–100] | – |
| 100 clients: connect+subscribe all (s) | 0.06 [0.06–0.06] | – |
| 100 clients: paced post→one client p50 ms | 2.44 [2.29–2.65] | – |
| 100 clients: paced post→all clients p50 ms | 2.71 [2.65–2.86] | – |
| 100 clients: paced post→all clients p99 ms | 4.54 [3.71–16.03] | – |
| 100 clients: max sustained msgs/s (delivered to all) | 0.70 [0.30–0.70] | – |
| 100 clients: deliveries/s (client×message) | 94,127 [82–112,755] | – |
| 100 clients: saturated post→all p50 ms | 13,304 [42–15,008] | – |
| 100 clients: saturated POST p50 ms | 0.74 [0.63–0.83] | – |
| 500 clients: subscribed | 500 [500–500] | – |
| 500 clients: connect+subscribe all (s) | 0.14 [0.11–0.16] | – |
| 500 clients: paced post→one client p50 ms | 3.70 [3.61–3.89] | – |
| 500 clients: paced post→all clients p50 ms | 5.00 [4.89–5.19] | – |
| 500 clients: paced post→all clients p99 ms | 8.29 [8.05–12.14] | – |
| 500 clients: max sustained msgs/s (delivered to all) | 0.90 [0.90–1.20] | – |
| 500 clients: deliveries/s (client×message) | 75,004 [58,565–79,478] | – |
| 500 clients: saturated post→all p50 ms | 124 [118–221] | – |
| 500 clients: saturated POST p50 ms | 0.65 [0.49–0.72] | – |
| 1000 clients: subscribed | 1,000 [1,000–1,000] | – |
| 1000 clients: connect+subscribe all (s) | 0.18 [0.17–0.18] | – |
| 1000 clients: paced post→one client p50 ms | 4.92 [4.92–5.05] | – |
| 1000 clients: paced post→all clients p50 ms | 8.01 [7.22–8.08] | – |
| 1000 clients: paced post→all clients p99 ms | 9.75 [9.73–12.81] | – |
| 1000 clients: max sustained msgs/s (delivered to all) | 0.60 [0.40–0.90] | – |
| 1000 clients: deliveries/s (client×message) | 5,008 [4,692–14,208] | – |
| 1000 clients: saturated post→all p50 ms | 15,032 [433–15,114] | – |
| 1000 clients: saturated POST p50 ms | 0.71 [0.50–0.78] | – |

### Upload + thumbnail (black_hole.jpg, 505 KB)

| Metric | Go (gina) | Go adv. |
|---|---|---|
| POST with attachment (ms) | 258 [251–259] | – |
| then GET thumb → 200 (ms) | 0.60 [0.50–0.70] | – |
| POST → thumbnail served (ms) | 258 [251–260] | – |

### Memory during cable fan-out, by process (MB, peak within the phase)

App process: Rails' Puma master and workers (Action Cable runs in them), or Go's one campfire
process (its front server included). Pss counts pages shared between forked workers once;
RssAnon counts them in every process.

| Metric | Go (gina) | Go adv. |
|---|---|---|
| 100 clients, all subscribed, idle: app process Pss | 448 [440–451] | – |
| 100 clients, all subscribed, idle: app process RssAnon | 432 [424–435] | – |
| 100 clients, all subscribed, idle: app + Redis + Thruster Pss | 448 [440–451] | – |
| 100 clients, all subscribed, idle: whole container Pss | 448 [440–451] | – |
| 100 clients, saturated fan-out: app process Pss | 508 [483–596] | – |
| 100 clients, saturated fan-out: app process RssAnon | 491 [467–580] | – |
| 100 clients, saturated fan-out: app + Redis + Thruster Pss | 508 [483–596] | – |
| 100 clients, saturated fan-out: whole container Pss | 508 [483–596] | – |
| 500 clients, all subscribed, idle: app process Pss | 484 [469–515] | – |
| 500 clients, all subscribed, idle: app process RssAnon | 468 [453–499] | – |
| 500 clients, all subscribed, idle: app + Redis + Thruster Pss | 484 [469–515] | – |
| 500 clients, all subscribed, idle: whole container Pss | 484 [469–515] | – |
| 500 clients, saturated fan-out: app process Pss | 1,115 [871–1,136] | – |
| 500 clients, saturated fan-out: app process RssAnon | 1,099 [855–1,120] | – |
| 500 clients, saturated fan-out: app + Redis + Thruster Pss | 1,115 [871–1,136] | – |
| 500 clients, saturated fan-out: whole container Pss | 1,115 [871–1,136] | – |
| 1000 clients, all subscribed, idle: app process Pss | 476 [468–479] | – |
| 1000 clients, all subscribed, idle: app process RssAnon | 460 [452–463] | – |
| 1000 clients, all subscribed, idle: app + Redis + Thruster Pss | 476 [468–479] | – |
| 1000 clients, all subscribed, idle: whole container Pss | 476 [468–479] | – |
| 1000 clients, saturated fan-out: app process Pss | 1,692 [1,582–2,049] | – |
| 1000 clients, saturated fan-out: app process RssAnon | 1,676 [1,566–2,033] | – |
| 1000 clients, saturated fan-out: app + Redis + Thruster Pss | 1,692 [1,582–2,049] | – |
| 1000 clients, saturated fan-out: whole container Pss | 1,692 [1,582–2,049] | – |
