# Debug log

Automated scenarios run with `go test -race ./...`. End-to-end scenarios publish a test stream with
`ffmpeg ... -f flv rtmp://127.0.0.1:1935/app/key` and use a stand-in `mpv` script placed next to the binary,
which starts the real mpv with `--vo=null --ao=null`.

| ID | Mode | Scenario | Expected |
|---|---|---|---|
| DBG-1 | Test | Measures are recorded while the log is off, then it is turned on | No file exists before; the file is named after the start of the session and holds the description of the session, the earlier lines, then the later ones |
| DBG-2 | Test | More than 500 lines are recorded while the log is off | The file only holds the latest 500 |
| DBG-3 | Test | The log is turned on, off, then on again | One file, which holds both parts and what happened in between |
| DBG-4 | Test | Any line of the log | It starts with the date and the time to the millisecond |
| DBG-5 | Test | mpv prints lines in pieces that stop anywhere, with `\r\n` and an empty line | Whole lines are recorded, no empty one |
| DBG-6 | Test | Type `d`, a note, then ` d ` in the console | The log is turned on, the note is in the file, the log is turned off |
| DBG-7 | Test | A stand-in mpv prints a line | The line is in the log under `mpv` |
| DBG-8 | Test | A steady stream; a picture held back 300 ms; a burst of pictures at the start; a second without any picture; an hour with the PC's clock 100 ppm faster than the PS5's | `late=0ms`; `longest_gap=320ms late=300ms`; lateness counted from the fastest picture; `pictures=0 longest_gap=1000ms`; still `late=0ms` |
| DBG-9 | Test | A reader reads nothing while 400 ms of stream arrive, then is brought back to a keyframe | `waiting=400ms … state=not started`, then `dropped=1000ms state=behind` |
| DBG-10 | Test | Report of a stream watched by two HTTP viewers; report without a stream | The pictures received and each viewer in the same order every time; no report |
| DBG-11 | Test | Load of the machine, read twice a second apart, then an hour later | Nothing the first time; then `cpu`, `busiest_core`, `ram`, `ram_free`, and the traffic as `down`/`up` in Mbps; nothing after the hour |
| DBG-12 | Test | Eight programs, the seventh busiest being ps5cast; the busiest programs of this machine | The five busiest and ps5cast are named; names only, never a command line |
| DBG-13 | Test | Windows GPU counters of two GPUs: two programs on one 3D engine and one on another, one decoding, one encoding on the second GPU, one idle | `gpu0_3D=42% gpu0_VideoDecode=8% gpu1_VideoEncode=20%` |
| DBG-14 | End-to-end | Start with `-debug`, publish 24 s of 1080p60, freeze mpv for 3 s then the publisher for 2 s (`kill -STOP`, then `kill -CONT`) | The file holds `stream`, `reader`, `pc` and `mpv` lines every second and `procs`, `net`, `gpu` every 5 s. The mpv freeze shows as `waiting` above 1000 ms, then `dropped` and `speed=1.10`; the publisher's as `longest_gap` and `late` near 2000 ms. The stream key is not in the file |
| DBG-15 | End-to-end | Start without `-debug`, publish, type a note, then `d` and Enter 12 s into the stream, then `d` and Enter again | The console prints the full path of the file. The file holds the lines from the start of the session, the note included, and stops growing once turned off |
| DBG-16 | Manual | During a real PS5 broadcast shown in the mpv window, type `d` then Enter, then a note | The console keeps working as before while mpv runs; the `mpv` lines show `hwdec` and a `vo` other than `null` |
| DBG-17 | Manual, Windows | Same as DBG-16 on Windows, with mpv installed by winget | No console window opens with mpv; the `mpv` lines are present; `gpu` shows `gpu0_3D`, `gpu0_VideoDecode`, `gpu0_VideoEncode`; `nic` is the name Windows gives the interface ("Wi-Fi", "Ethernet") |
| DBG-18 | End-to-end | Play a clip with the real mpv and `mpv-stats.lua`: in a window, then started minimized (`--window-minimized=yes`) | The `mpv` lines end with `pause=no focused=yes minimized=no` (`focused=no` when the system did not give the window the focus), then `minimized=yes` |
| DBG-19 | End-to-end, macOS | Play a 6 s 720p60 clip minimized, with and without `mpv-catchup.lua` (which sets `force-render`), and compare mpv's CPU time (`/usr/bin/time -p`) | Without it mpv uses about 40% less CPU than in a visible window (it stops drawing); with it, as much as in a visible window |
| DBG-20 | Manual | During a real broadcast shared in Discord, put the mpv window behind another one, then minimize it, with the debug log on | `focused=no` then `minimized=yes` in the `mpv` lines; the picture in Discord keeps moving while the window is behind the other one |
