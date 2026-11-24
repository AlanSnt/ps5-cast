# ps5cast

Show your PS5's video feed in a window on your PC (Windows, macOS or Linux), ready to share on Discord.
No capture card, no Remote Play, no router setup.

## How it works

The PS5 believes it is broadcasting to Twitch:
1. you set the PC's IP as the PS5's **primary DNS**;
2. ps5cast answers the DNS queries: Twitch ingest domains point to the PC, everything else is relayed to 1.1.1.1.
   The ingest domains are the built-in ones plus the list Twitch publishes, read at startup;
3. the PS5 sends its RTMP stream (1080p60 H.264) to the PC on port 1935;
4. ps5cast serves it, without re-encoding, to:
   - **mpv**, if installed (a "PS5" window): the recommended way. It has the lowest latency and decodes on the GPU when it can, which keeps the picture in sync with the sound while Discord captures the window. When the window falls behind (busy PC, window being moved), it comes back to the live picture on its own: mpv plays slightly faster for a few seconds, and past about a second of delay the picture skips ahead. ffplay is used when mpv is missing;
   - **the built-in web page** `http://localhost:8080` (Chrome/Edge, about 200 ms of buffer, nothing to install);
   - **OBS / VLC** through `http://<pc-ip>:8080/live.flv`.

Nothing is sent to Twitch. The stream key is never displayed or stored.

## Download

Pick the file for your system from the [latest release](https://github.com/AlanSnt/ps5-cast/releases/latest):

| System | File |
|---|---|
| Windows | [`ps5cast-windows-amd64.exe`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-windows-amd64.exe) |
| macOS Apple Silicon (M1 and later) | [`ps5cast-darwin-arm64.tar.gz`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-darwin-arm64.tar.gz) |
| macOS Intel | [`ps5cast-darwin-amd64.tar.gz`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-darwin-amd64.tar.gz) |
| Linux x86-64 | [`ps5cast-linux-amd64.tar.gz`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-linux-amd64.tar.gz) |

Requires Windows 10, macOS 13 or a 64-bit Linux, or later. The binaries are not signed: Windows and macOS show a warning on first launch.

At startup, a released binary asks GitHub for the latest stable release and prints a link in the console when a newer one exists. It downloads nothing by itself.

## Setup

1. *(Recommended)* Install mpv, the best way to watch and share the stream: `winget install shinchiro.mpv` (Windows), `brew install mpv` (macOS) or your distribution's `mpv` package (Linux). You can also drop the mpv binary next to ps5cast. Without mpv, the web page opens on its own.
2. Start ps5cast:
   - **Windows**: double-click `ps5cast-windows-amd64.exe`. If SmartScreen blocks it: More info → Run anyway. When Windows asks, allow access on **private networks**.
   - **macOS**: in a terminal, from the download folder:
     ```sh
     tar -xzf ps5cast-darwin-arm64.tar.gz    # skip if Safari already extracted the archive
     xattr -d com.apple.quarantine ps5cast   # lifts the Gatekeeper block
     ./ps5cast
     ```
     On an Intel Mac, replace `arm64` with `amd64`. If the macOS firewall asks, allow incoming connections.
   - **Linux**: port 53 is reserved for root, so grant the binary that right once:
     ```sh
     tar -xzf ps5cast-linux-amd64.tar.gz
     sudo setcap cap_net_bind_service=+ep ./ps5cast
     ./ps5cast
     ```
3. On the PS5, link your Twitch account first: Settings → Users and Accounts → Link with Other Services → Twitch. Do it **before** changing the DNS: the account must already be signed in when the PS5 starts using the PC as its DNS.
4. On the PS5: Settings → Network → Settings → Set Up Internet Connection → your connection → Advanced Settings
   - DNS Settings: **Manual**
   - Primary DNS: the IP printed by ps5cast
   - Secondary DNS: `1.1.1.1` (Internet keeps working when the PC is off)
5. Start a Twitch broadcast with the Create button.
6. The mpv window (or the page) shows the game. In Discord: Share Your Screen → the "PS5" window, or the browser window.

Web page: click = enable sound (the browser starts muted), **f** fullscreen, **m** mute, **s** latency stats.
Console: **Enter** restarts the mpv window, **d** then Enter turns the [debug log](#debug-log) on or off, **Ctrl+C** quits.

### OBS / VLC
- OBS: Sources → **Media Source** → uncheck "Local File" → Input: `http://<pc-ip>:8080/live.flv`.
- VLC: Media → Open Network Stream → the same URL.

## Options

| Option | Default | Purpose |
|---|---|---|
| `-ip` | auto | local IP of the PC |
| `-upstream` | `1.1.1.1:53` | public DNS for all other queries |
| `-spoof` | `*.contribute.live-video.net,*.global-contribute.live-video.net,live*.twitch.tv` + Twitch's published ingests | exact list of domains redirected to the PC; when set, Twitch's list is not read |
| `-player` | `auto` | `mpv`, `ffplay` or `none` |
| `-http` | `:8080` | web page and HTTP-FLV stream (empty = disabled) |
| `-open` | `auto` | open the page at startup: `auto` (if no mpv), `always`, `never` |
| `-record` | — | also record the stream to a `.flv` file; an existing file is never overwritten (`game-2.flv`, `game-3.flv`…) |
| `-probe443` | `true` | listen on port 443 to detect an RTMPS attempt; pass `-probe443=false` if another program needs that port |
| `-v` | off | log every DNS query, and why the check for a newer version failed |
| `-debug` | off | write the [debug log](#debug-log) from the start |

## Debug log

When the picture lags now and then, the debug log records what is needed to find out why. It is one file per session, `ps5cast-debug-<date>-<time>.log`, created in the folder ps5cast was started from; the console prints its full path.

- **From the start**: run `ps5cast -debug`.
- **While it lags**: type `d` then Enter in the console. During a stream, ps5cast always keeps the last two minutes or so in memory, so the file also holds what happened *before* you turned it on. `d` then Enter again turns it off; turning it on again continues the same file.
- **To mark a moment**: type a few words then Enter (`lag in the menu`). They are written to the log with the time, which helps when the log runs for a whole evening.

Every line starts with the date, the time to the millisecond, and where the measure comes from:

| Source | Every | What it tells |
|---|---|---|
| `log` | — | the console messages, and your notes |
| `stream` | 1 s | how the PS5's stream arrives: bitrate, pictures and keyframes received, `longest_gap` between two pictures, and `late`, how much later than the fastest picture the latest-coming one arrived. A Wi-Fi that stalls shows here |
| `reader` | 1 s | for the mpv window and each viewer of the page: the stream it has not read yet (`waiting`), the timestamp of the last picture it was sent (`sent`), what was skipped to bring it back to the live picture (`dropped`) |
| `mpv` | 1 s | from the mpv window: playback position (`pos`), speed, sound/picture offset (`avsync`), stream waiting in mpv, pictures per second, pictures dropped, GPU decoding in use (`hwdec`), whether the window is paused (`pause`), has the focus (`focused`) or is minimized (`minimized`), and mpv's own warnings |
| `pc` | 1 s | CPU (average and busiest core), memory, and the traffic of the network interface the PS5 reaches the PC through (`down`/`up`, lost packets) |
| `procs` | 5 s | the five programs that use the most CPU, plus mpv and ps5cast, as shares of one core (250% = two cores and a half) |
| `net` | 5 s | how long the public DNS (`-upstream`) takes to answer: a saturated Internet link shows here |
| `gpu` | 5 s | GPU load. Windows: per GPU and kind of work (`gpu0_3D`, `gpu0_VideoDecode` for mpv, `gpu0_VideoEncode` for Discord). macOS: overall. Linux: AMD only. Only measured while the log is on |

The file never contains the stream key. It does contain everything the console prints, so the local IP addresses of the PC, of the PS5 and of the devices that watched, and the domains the PS5 looked up (all of them with `-v`); and also the PC's model, the options ps5cast was started with and the names of the busiest programs (never their command line). Read it before sending it to someone. It grows by about 2 MB per hour.

The page in the browser does not report its own playback: with it, the `reader` lines tell whether the browser reads the stream as fast as it arrives.

## Troubleshooting

- **It lags now and then**: turn on the [debug log](#debug-log) while it lags (`d` then Enter), let it run a minute, and attach the file when reporting the problem.

- **"port 53 is already in use"**: another service holds the port. Stop it, or run ps5cast on another machine.
  - Windows: connection sharing, Docker or Hyper-V.
  - Linux: usually `systemd-resolved`. Set `DNSStubListener=no` in `/etc/systemd/resolved.conf`, then:
    ```sh
    sudo ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf   # without this the PC loses its own DNS
    sudo systemctl restart systemd-resolved
    ```
- **Linux, "permission denied" on port 53**: the `setcap` command from the setup was not run (run it again after every update of the binary).
- **The PS5 broadcasts but nothing arrives**: look at the `dns …` lines in the console. If a new ingest domain shows up, add it with `-spoof "*.contribute.live-video.net,*.global-contribute.live-video.net,live*.twitch.tv,new.domain"`; a `-spoof` list replaces the automatic one, Twitch's published list included. A "Twitch ingest list unavailable" line at startup means only the built-in domains are redirected.
- **"closed without a stream after: …" message**: a device reached the PC over RTMP but no stream followed. The list is how far the exchange went (commands the RTMP library does not know are not listed); include that line when reporting the problem, it never contains the stream key. A "refused, another one is already in progress" line just before means ps5cast itself turned the stream down.
- **"the player was 2.3 s behind: back to the live picture" message**: the window could not keep up for a moment, so ps5cast skipped the late pictures instead of leaving it behind for good. Now and then it is harmless. When it comes back every ten seconds, the PC struggles to play the stream: close what keeps it busy, or use the web page.
- **The window is frozen, or stays late**: press **Enter** in the console. The window is restarted, so share it again in Discord. The console says "the player has stopped reading the stream (paused or frozen)" when mpv takes nothing anymore.
- **The picture shared in Discord freezes while the window is hidden**: mpv keeps drawing a window that is behind other windows or minimized, but Discord itself cannot capture a minimized window on Windows and macOS. Keep the window open, behind the game or on another screen.
- **"attempts a TLS connection on :443" message**: the PS5 is trying RTMPS (encrypted), which cannot be intercepted. The domain logged just before must not be redirected: pass `-spoof` with the list of domains without that one.
- **"host not allowed" (403) on the web page or in OBS**: open it with `localhost` or the PC's IP address. The console prints the refused name. A machine name such as `my-pc.local` is refused on purpose: it is what stops another website from reading the stream through DNS rebinding.
- **Nothing at all**: check the PC's firewall (inbound ports 53 UDP/TCP and 1935 TCP; private profile on Windows).

## Build

```sh
go build -trimpath -ldflags="-s -w" .
```

For another platform, add `GOOS` and `GOARCH` (for example `GOOS=windows GOARCH=amd64`).

Dependencies: `github.com/miekg/dns`, `github.com/shirou/gopsutil` and `github.com/yusufpapurcu/wmi` (the CPU, memory, network, process and Windows GPU measures of the debug log) and `github.com/yutopp/go-rtmp`, the latter through the fork [`AlanSnt/go-rtmp`](https://github.com/AlanSnt/go-rtmp): the PS5 sends its RTMP `connect` command with transaction ID 0 and only accepts an answer carrying the same ID, which the upstream library does not send. The web player uses [mpegts.js](https://github.com/xqq/mpegts.js) 1.8.2 (Apache-2.0), embedded in the binary.

### Publishing a release

Push a `vX.Y.Z` tag: the [`build.yml`](.github/workflows/build.yml) workflow compiles the four binaries and attaches them to a GitHub release. The tag becomes the version printed at startup.

A tag with a suffix, such as `v0.2.0-alpha`, is published as a pre-release: it never becomes the latest release, so the download links above keep pointing to the last stable version.

```sh
git tag v0.2.0
git push origin v0.2.0
```

## License

[MIT](LICENSE). The embedded mpegts.js is Apache-2.0; its license text ships inside the binary and is served at `/mpegts.js.LICENSE.txt` (source: [`web/mpegts.js.LICENSE.txt`](web/mpegts.js.LICENSE.txt)).

ps5cast is not affiliated with Sony Interactive Entertainment or Twitch.
