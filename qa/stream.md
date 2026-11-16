# Stream pipeline

Automated scenarios run with `go test -race ./...`. End-to-end scenarios publish a test stream with
`ffmpeg ... -f flv rtmp://127.0.0.1:1935/app/key` and use a stand-in `mpv` script placed next to the binary.

| ID | Mode | Scenario | Expected |
|---|---|---|---|
| STR-1 | Test | Request `/live.flv` during a stream | No `Access-Control-Allow-Origin` header |
| STR-2 | Test | The local player stops reading | `Sink.Write` keeps returning and the player is dropped |
| STR-3 | Test | Record three times to the same path | `game.flv`, `game-2.flv`, `game-3.flv`; the first one is untouched |
| STR-4 | Test | The recording file fails to write | The error is logged and the recording is closed |
| STR-5 | Test | An HTTP viewer never reads the stream | Its handler returns after the write deadline |
| STR-6 | Test | `-ip` set to `192.168.1.300`, `eth0` or `::1` | Startup fails with "is not an IPv4 address" |
| STR-7 | Test | `-http` on all interfaces, on loopback, on one interface, on port 0 | The printed URLs match the address really bound |
| STR-8 | Test | Banner with ffplay, without a player, without a web page | It names the player in use, or warns that nothing displays the stream |
| STR-9 | End-to-end | Publish 5 s then 3 s with `-record rec.flv` | `rec.flv` (5 s) and `rec-2.flv` (3 s) are valid H.264/AAC; the player and `/live.flv` receive the stream; the stream key is not logged |
| STR-10 | End-to-end | Publish 10 s at 60 fps to a player that never reads | "player closed or stalled" is logged after about 4 s, the publisher is not slowed and `/live.flv` still serves the stream |
| STR-11 | Manual | Pause a real mpv window during a PS5 broadcast | The window closes after about 4 s; Enter reopens it |
| STR-12 | Manual | Real PS5 broadcast shown in the browser and in mpv | Video and sound play with low latency |
