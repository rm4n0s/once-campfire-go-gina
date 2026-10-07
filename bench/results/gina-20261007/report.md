```
date: 2026-10-07T23:10:05+03:00
host: 7.2.9-300.vanilla.fc44.x86_64, AMD RYZEN AI MAX+ 395 w/ Radeon 8060S, 32 threads, 30GB
server cpus: 8-11 (nproc 4); loadgen cpus: 12-15; network: host
env: WEB_CONCURRENCY=3 JOB_CONCURRENCY=3 RAILS_MAX_THREADS=5 
gina extra env: 
user agent: (none)
reference image: campfire-reference:app sha256:80676d9912f5f473b1347a951efef3a0b391859d9c1b225f1218a3690c4629d9 2026-10-07T20:13:19.000950813+03:00
gina image: campfire-gina:app sha256:2da962596e67ada657381c3f09cc6b96da6ca175ed942b9edbfcb539abb031fe 2026-10-07T22:50:23.702610604+03:00
gina HEAD: 6ea7319 (dirty: 14 files)
```

Reps: reference 3, gina 3. Cells: median [min–max].

### Startup and memory

| Metric | Rails | Go (gina) | Go adv. |
|---|---|---|---|
| cold start: docker run → /up 200 (ms) | 2,845 [2,653–2,855] | 152 [143–155] | 18.7× |
| idle memory.current (MB) | 300 [299–303] | 27.0 [27.0–28.0] | 11.1× |
| idle anon (MB) | 279 [278–280] | 25.0 [25.0–25.0] | 11.2× |
| peak memory.current under load (MB) | 1,480 [1,451–1,487] | 1,486 [1,470–1,567] | 1.0× |
| peak anon under load (MB) | 1,439 [1,408–1,444] | 1,473 [1,405–1,486] | 1.0× |

### HTTP (signed in as david; keep-alive; c = concurrent connections)

| Metric | Rails | Go (gina) | Go adv. |
|---|---|---|---|
| room_show c=1 req/s | 80.4 [79.6–88.9] | 3,291 [3,291–3,307] | 40.9× |
| room_show c=1 p50 ms | 10.9 [10.8–11.0] | 0.29 [0.29–0.29] | 37.6× |
| room_show c=1 p99 ms | 33.2 [13.9–33.6] | 0.56 [0.55–0.60] | 59.1× |
| room_show c=16 req/s | 203 [203–210] | 9,971 [9,873–10,063] | 49.0× |
| room_show c=16 p50 ms | 75.7 [74.6–76.7] | 1.14 [1.13–1.16] | 66.3× |
| room_show c=16 p99 ms | 202 [156–205] | 6.66 [6.42–7.20] | 30.3× |
| room_show c=64 req/s | 174 [174–177] | 9,812 [9,795–9,832] | 56.2× |
| room_show c=64 p50 ms | 364 [359–373] | 5.95 [5.84–6.16] | 61.1× |
| room_show c=64 p99 ms | 484 [479–488] | 14.7 [14.0–15.6] | 33.0× |
| messages_page c=1 req/s | 165 [158–166] | 3,776 [3,732–3,794] | 22.9× |
| messages_page c=1 p50 ms | 5.92 [5.82–5.93] | 0.26 [0.26–0.26] | 23.0× |
| messages_page c=1 p99 ms | 8.07 [8.05–14.41] | 0.36 [0.36–0.38] | 22.1× |
| messages_page c=16 req/s | 396 [396–398] | 13,082 [12,767–13,207] | 33.0× |
| messages_page c=16 p50 ms | 32.6 [25.4–34.8] | 1.21 [1.00–1.22] | 27.0× |
| messages_page c=16 p99 ms | 113 [104–122] | 4.88 [4.79–4.97] | 23.3× |
| messages_page c=64 req/s | 362 [361–367] | 12,969 [12,943–13,091] | 35.8× |
| messages_page c=64 p50 ms | 172 [171–172] | 4.41 [4.17–4.46] | 38.9× |
| messages_page c=64 p99 ms | 264 [256–272] | 11.4 [11.2–11.5] | 23.2× |
| sidebar c=1 req/s | 190 [188–209] | 5,432 [5,308–5,481] | 28.6× |
| sidebar c=1 p50 ms | 4.65 [4.62–4.67] | 0.18 [0.18–0.18] | 26.0× |
| sidebar c=1 p99 ms | 20.0 [6.8–21.0] | 0.23 [0.22–0.23] | 87.2× |
| sidebar c=16 req/s | 517 [484–525] | 18,311 [18,233–19,602] | 35.4× |
| sidebar c=16 p50 ms | 30.4 [29.8–30.8] | 0.77 [0.73–0.83] | 39.6× |
| sidebar c=16 p99 ms | 59.6 [56.4–116.3] | 2.39 [2.18–2.45] | 25.0× |
| sidebar c=64 req/s | 503 [450–516] | 18,298 [18,294–19,315] | 36.4× |
| sidebar c=64 p50 ms | 125 [122–131] | 3.47 [3.20–3.54] | 35.9× |
| sidebar c=64 p99 ms | 235 [167–246] | 7.49 [7.11–7.67] | 31.4× |
| search c=1 req/s | 163 [161–163] | 6,085 [6,060–6,101] | 37.3× |
| search c=1 p50 ms | 5.94 [5.93–6.00] | 0.16 [0.16–0.16] | 37.1× |
| search c=1 p99 ms | 8.46 [8.19–8.48] | 0.21 [0.21–0.22] | 39.7× |
| search c=16 req/s | 355 [355–380] | 22,853 [22,322–22,908] | 64.4× |
| search c=16 p50 ms | 42.1 [41.2–44.7] | 0.76 [0.73–0.85] | 55.3× |
| search c=16 p99 ms | 83.5 [73.5–92.9] | 2.87 [2.78–3.03] | 29.1× |
| search c=64 req/s | 358 [333–362] | 22,707 [22,562–22,737] | 63.5× |
| search c=64 p50 ms | 176 [174–188] | 2.64 [2.44–2.86] | 66.6× |
| search c=64 p99 ms | 261 [209–265] | 6.97 [6.75–7.10] | 37.5× |
| avatar c=1 req/s | 27,357 [27,139–27,403] | 9,882 [9,831–10,192] | 0.4× |
| avatar c=1 p50 ms | 0.03 [0.03–0.03] | 0.10 [0.10–0.10] | 0.3× |
| avatar c=1 p99 ms | 0.10 [0.10–0.10] | 0.12 [0.12–0.14] | 0.8× |
| avatar c=16 req/s | 93,531 [92,906–93,838] | 41,421 [41,349–42,736] | 0.4× |
| avatar c=16 p50 ms | 0.11 [0.11–0.11] | 0.34 [0.32–0.47] | 0.3× |
| avatar c=16 p99 ms | 0.91 [0.90–0.92] | 1.00 [0.99–1.00] | 0.9× |
| avatar c=64 req/s | 74,122 [73,739–75,178] | 42,846 [42,486–43,304] | 0.6× |
| avatar c=64 p50 ms | 0.33 [0.33–0.34] | 1.43 [1.35–1.44] | 0.2× |
| avatar c=64 p99 ms | 5.12 [5.09–5.20] | 4.38 [4.18–4.59] | 1.2× |
| static_css c=1 req/s | 33,457 [33,353–33,564] | 34,054 [33,867–34,085] | 1.0× |
| static_css c=1 p50 ms | 0.03 [0.03–0.03] | 0.03 [0.03–0.03] | 1.0× |
| static_css c=1 p99 ms | 0.08 [0.08–0.08] | 0.04 [0.04–0.04] | 2.1× |
| static_css c=16 req/s | 124,436 [124,186–126,896] | 330,704 [319,196–336,547] | 2.7× |
| static_css c=16 p50 ms | 0.09 [0.09–0.09] | 0.04 [0.03–0.04] | 2.5× |
| static_css c=16 p99 ms | 0.69 [0.66–0.70] | 0.11 [0.11–0.11] | 6.3× |
| static_css c=64 req/s | 104,323 [103,467–104,728] | 341,426 [333,878–345,208] | 3.3× |
| static_css c=64 p50 ms | 0.30 [0.29–0.31] | 0.15 [0.14–0.15] | 2.0× |
| static_css c=64 p99 ms | 3.57 [3.56–3.65] | 1.31 [1.20–1.51] | 2.7× |
| up c=1 req/s | 1,511 [1,475–1,530] | 29,071 [28,830–29,218] | 19.2× |
| up c=1 p50 ms | 0.64 [0.64–0.66] | 0.03 [0.03–0.03] | 19.5× |
| up c=1 p99 ms | 1.22 [1.22–1.31] | 0.04 [0.04–0.04] | 28.4× |
| up c=16 req/s | 3,671 [3,661–3,716] | 199,761 [170,974–220,990] | 54.4× |
| up c=16 p50 ms | 4.14 [4.10–4.20] | 0.08 [0.06–0.09] | 54.5× |
| up c=16 p99 ms | 9.06 [8.90–9.29] | 0.14 [0.12–0.15] | 67.1× |
| up c=64 req/s | 3,874 [3,864–3,917] | 222,123 [219,469–223,922] | 57.3× |
| up c=64 p50 ms | 16.4 [16.2–16.4] | 0.26 [0.23–0.27] | 63.7× |
| up c=64 p99 ms | 25.1 [24.9–25.2] | 2.31 [2.29–2.40] | 10.9× |
| post_message c=1 req/s | 141 [129–142] | 284 [41–2,575] | 2.0× |
| post_message c=1 p50 ms | 6.69 [6.65–7.15] | 0.43 [0.32–0.43] | 15.6× |
| post_message c=1 p99 ms | 12.0 [11.6–13.1] | 81.5 [2.7–416.8] | 0.1× |
| post_message c=16 req/s | 266 [262–268] | 594 [53–3,210] | 2.2× |
| post_message c=16 p50 ms | 57.9 [48.3–59.0] | 3.68 [3.19–4.23] | 15.7× |
| post_message c=16 p99 ms | 177 [153–178] | 150 [39–3,244] | 1.2× |
| post_message c=64 req/s | 262 [253–268] | 2,910 [83–3,272] | 11.1× |
| post_message c=64 p50 ms | 241 [234–252] | 15.5 [15.0–687.6] | 15.5× |
| post_message c=64 p99 ms | 377 [351–381] | 214 [122–1,478] | 1.8× |

### HTTP errors / non-2xx-3xx (first rep, per app)

| Metric | Rails | Go (gina) | Go adv. |
|---|---|---|---|
- reference: none
- gina: none

### Action Cable fan-out (one room; chatter.js subscriptions per client)

| Metric | Rails | Go (gina) | Go adv. |
|---|---|---|---|
| 100 clients: subscribed | 100 [100–100] | 100 [100–100] | 1.0× |
| 100 clients: connect+subscribe all (s) | 0.32 [0.27–0.32] | 0.06 [0.06–0.07] | 5.3× |
| 100 clients: paced post→one client p50 ms | 17.4 [17.2–17.5] | 2.14 [2.10–2.23] | 8.1× |
| 100 clients: paced post→all clients p50 ms | 23.8 [23.6–24.9] | 2.52 [2.40–2.52] | 9.4× |
| 100 clients: paced post→all clients p99 ms | 44.4 [35.4–66.4] | 3.36 [2.96–14.85] | 13.2× |
| 100 clients: max sustained msgs/s (delivered to all) | 77.3 [76.0–77.3] | 0.50 [0.40–0.60] | 0.0× |
| 100 clients: deliveries/s (client×message) | 7,729 [7,603–7,734] | 79,773 [36,493–171,304] | 10.3× |
| 100 clients: saturated post→all p50 ms | 53.8 [52.5–60.5] | 3,647 [1,697–3,770] | 0.0× |
| 100 clients: saturated POST p50 ms | 39.4 [30.9–42.9] | 0.74 [0.63–0.79] | 53.1× |
| 500 clients: subscribed | 500 [500–500] | 500 [500–500] | 1.0× |
| 500 clients: connect+subscribe all (s) | 0.92 [0.91–1.02] | 0.12 [0.11–0.14] | 7.7× |
| 500 clients: paced post→one client p50 ms | 31.4 [31.1–33.0] | 3.42 [3.30–3.46] | 9.2× |
| 500 clients: paced post→all clients p50 ms | 61.5 [60.6–64.3] | 4.69 [4.68–4.83] | 13.1× |
| 500 clients: paced post→all clients p99 ms | 132 [117–147] | 11.0 [8.3–18.5] | 12.0× |
| 500 clients: max sustained msgs/s (delivered to all) | 22.5 [22.5–23.2] | 1.00 [1.00–1.30] | 0.0× |
| 500 clients: deliveries/s (client×message) | 11,270 [11,252–11,599] | 10,304 [1,902–166,246] | 0.9× |
| 500 clients: saturated post→all p50 ms | 381 [179–2,228] | 15,016 [15,008–15,024] | 0.0× |
| 500 clients: saturated POST p50 ms | 83.5 [74.7–119.2] | 0.66 [0.57–0.84] | 127.0× |
| 1000 clients: subscribed | 1,000 [1,000–1,000] | 1,000 [1,000–1,000] | 1.0× |
| 1000 clients: connect+subscribe all (s) | 1.76 [1.75–1.78] | 0.18 [0.17–0.18] | 9.8× |
| 1000 clients: paced post→one client p50 ms | 42.3 [39.8–42.7] | 4.61 [4.57–4.65] | 9.2× |
| 1000 clients: paced post→all clients p50 ms | 83.3 [79.3–84.5] | 7.81 [7.55–7.81] | 10.7× |
| 1000 clients: paced post→all clients p99 ms | 208 [203–210] | 8.83 [8.66–9.12] | 23.5× |
| 1000 clients: max sustained msgs/s (delivered to all) | 12.9 [11.9–13.2] | 0.60 [0.50–0.60] | 0.0× |
| 1000 clients: deliveries/s (client×message) | 12,871 [11,855–13,200] | 4,373 [4,046–109,204] | 0.3× |
| 1000 clients: saturated post→all p50 ms | 293 [250–1,060] | 15,057 [15,032–15,114] | 0.0× |
| 1000 clients: saturated POST p50 ms | 219 [171–232] | 0.77 [0.75–0.94] | 282.8× |

### Upload + thumbnail (black_hole.jpg, 505 KB)

| Metric | Rails | Go (gina) | Go adv. |
|---|---|---|---|
| POST with attachment (ms) | 78.0 [76.0–105.2] | 272 [264–274] | 0.3× |
| then GET thumb → 200 (ms) | 0.60 [0.50–0.70] | 0.60 [0.60–0.70] | 1.0× |
| POST → thumbnail served (ms) | 78.6 [76.7–105.8] | 273 [264–274] | 0.3× |

### Memory during cable fan-out, by process (MB, peak within the phase)

App process: Rails' Puma master and workers (Action Cable runs in them), or Go's one campfire
process (its front server included). Pss counts pages shared between forked workers once;
RssAnon counts them in every process.

| Metric | Rails | Go (gina) | Go adv. |
|---|---|---|---|
| 100 clients, all subscribed, idle: app process Pss | 558 [550–560] | 358 [200–360] | 1.6× |
| 100 clients, all subscribed, idle: app process RssAnon | 705 [698–706] | 342 [184–344] | 2.1× |
| 100 clients, all subscribed, idle: app + Redis + Thruster Pss | 597 [588–600] | 358 [200–360] | 1.7× |
| 100 clients, all subscribed, idle: whole container Pss | 868 [859–875] | 358 [200–360] | 2.4× |
| 100 clients, saturated fan-out: app process Pss | 656 [654–662] | 413 [369–501] | 1.6× |
| 100 clients, saturated fan-out: app process RssAnon | 803 [801–807] | 397 [353–485] | 2.0× |
| 100 clients, saturated fan-out: app + Redis + Thruster Pss | 707 [704–714] | 413 [369–501] | 1.7× |
| 100 clients, saturated fan-out: whole container Pss | 979 [977–989] | 413 [369–501] | 2.4× |
| 500 clients, all subscribed, idle: app process Pss | 614 [611–620] | 370 [318–377] | 1.7× |
| 500 clients, all subscribed, idle: app process RssAnon | 759 [757–763] | 354 [302–361] | 2.1× |
| 500 clients, all subscribed, idle: app + Redis + Thruster Pss | 682 [678–689] | 370 [318–377] | 1.8× |
| 500 clients, all subscribed, idle: whole container Pss | 954 [951–966] | 370 [318–377] | 2.6× |
| 500 clients, saturated fan-out: app process Pss | 802 [794–882] | 831 [741–861] | 1.0× |
| 500 clients, saturated fan-out: app process RssAnon | 947 [937–1,027] | 816 [725–845] | 1.2× |
| 500 clients, saturated fan-out: app + Redis + Thruster Pss | 880 [874–960] | 831 [741–861] | 1.1× |
| 500 clients, saturated fan-out: whole container Pss | 1,152 [1,149–1,232] | 831 [741–861] | 1.4× |
| 1000 clients, all subscribed, idle: app process Pss | 673 [672–692] | 538 [494–559] | 1.3× |
| 1000 clients, all subscribed, idle: app process RssAnon | 817 [814–837] | 522 [478–543] | 1.6× |
| 1000 clients, all subscribed, idle: app + Redis + Thruster Pss | 812 [794–828] | 538 [494–559] | 1.5× |
| 1000 clients, all subscribed, idle: whole container Pss | 1,084 [1,070–1,101] | 538 [494–559] | 2.0× |
| 1000 clients, saturated fan-out: app process Pss | 983 [974–999] | 1,489 [1,379–1,497] | 0.7× |
| 1000 clients, saturated fan-out: app process RssAnon | 1,125 [1,118–1,144] | 1,473 [1,363–1,481] | 0.8× |
| 1000 clients, saturated fan-out: app + Redis + Thruster Pss | 1,118 [1,112–1,140] | 1,489 [1,379–1,497] | 0.8× |
| 1000 clients, saturated fan-out: whole container Pss | 1,388 [1,384–1,409] | 1,489 [1,379–1,497] | 0.9× |
