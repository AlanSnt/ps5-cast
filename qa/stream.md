# Stream pipeline

Automated scenarios run with `go test -race ./...`. End-to-end scenarios publish a test stream with
`ffmpeg ... -f flv rtmp://127.0.0.1:1935/app/key` and use a stand-in `mpv` script placed next to the binary.

| ID | Mode | Scenario | Expected |
|---|---|---|---|
| STR-1 | Test | Request `/live.flv` during a stream | No `Access-Control-Allow-Origin` header |
| STR-2 | Test | The local player stops reading | `Sink.Write` keeps returning and the player is dropped |
| STR-3 | Test | Record three times to the same path, then to a directory | `game.flv`, `game-2.flv`, `game-3.flv`, the first one untouched; the directory is refused |
| STR-4 | Test | The recording file fails to write, or fails when closed | The error is logged |
| STR-5 | Test | An HTTP viewer never reads the stream | Its handler returns after the write deadline |
| STR-6 | Test | `-ip` set to `192.168.1.300`, `eth0` or `::1` | Startup fails with "is not an IPv4 address" |
| STR-7 | Test | `-http` on all interfaces, on loopback, on one interface, on port 0 | The printed URLs match the address really bound |
| STR-8 | Test | Banner with ffplay, without a player, without a web page | It names the player in use, or warns that nothing displays the stream |
| STR-13 | Test | Request `/status` with a `Host` of `localhost`, an IP, `evil.example`, `my-pc.local` | 200 for localhost and IPs, 403 for the names |
| STR-14 | Test | Resolve `cdg10.contribute.live-video.net`, `ingest.global-contribute.live-video.net`, `ingest.twitch.tv` with the built-in domains | The two ingests are redirected, `ingest.twitch.tv` is not |
| STR-15 | Test | Twitch's ingest list holds a host the built-in domains do not cover, a wildcard host and an RTMPS-only one | Only the plain RTMP host is added to the redirected domains |
| STR-16 | Test | Twitch's ingest list answers 500, is not JSON, or is empty | The built-in domains are used alone |
| STR-17 | Test | `-spoof` is set | It is the exact list of redirected domains and Twitch's list is not fetched |
| STR-18 | Test | An RTMP client connects, creates a stream and leaves without publishing | The console logs "left without sending a stream after: connect, createStream" |
| STR-9 | End-to-end | Publish 5 s then 3 s with `-record rec.flv` | `rec.flv` (5 s) and `rec-2.flv` (3 s) are valid H.264/AAC; the player and `/live.flv` receive the stream; the stream key is not logged |
| STR-10 | End-to-end | Publish 10 s at 60 fps to a player that never reads | "player closed or stalled" is logged after about 10 s, the publisher is not slowed and `/live.flv` still serves the stream |
| STR-11 | Manual | Pause a real mpv window during a PS5 broadcast | The window closes after about 10 s; Enter reopens it |
| STR-12 | Manual | Real PS5 broadcast shown in the browser and in mpv | Video and sound play with low latency |
