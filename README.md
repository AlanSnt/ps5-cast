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
   - **the built-in web page** `http://localhost:8080` (Chrome/Edge, about 200 ms of buffer, nothing to install);
   - **mpv** or ffplay if installed (a "PS5" window, lowest latency);
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

1. *(Optional)* Install mpv: `winget install mpv` (Windows), `brew install mpv` (macOS) or your distribution's `mpv` package (Linux). You can also drop the mpv binary next to ps5cast. Without mpv, the web page opens on its own.
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
6. The page (or the mpv window) shows the game. In Discord: Share Your Screen → the browser window or "PS5".

Web page: click = enable sound (the browser starts muted), **f** fullscreen, **m** mute, **s** latency stats.
Console: **Enter** reopens the mpv window, **Ctrl+C** quits.

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

## Troubleshooting

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
- **"attempts a TLS connection on :443" message**: the PS5 is trying RTMPS (encrypted), which cannot be intercepted. The domain logged just before must not be redirected: pass `-spoof` with the list of domains without that one.
- **"host not allowed" (403) on the web page or in OBS**: open it with `localhost` or the PC's IP address. The console prints the refused name. A machine name such as `my-pc.local` is refused on purpose: it is what stops another website from reading the stream through DNS rebinding.
- **Nothing at all**: check the PC's firewall (inbound ports 53 UDP/TCP and 1935 TCP; private profile on Windows).

## Build

```sh
go build -trimpath -ldflags="-s -w" .
```

For another platform, add `GOOS` and `GOARCH` (for example `GOOS=windows GOARCH=amd64`).

Dependencies: `github.com/miekg/dns` and `github.com/yutopp/go-rtmp`, the latter through the fork [`AlanSnt/go-rtmp`](https://github.com/AlanSnt/go-rtmp): the PS5 sends its RTMP `connect` command with transaction ID 0 and only accepts an answer carrying the same ID, which the upstream library does not send. The web player uses [mpegts.js](https://github.com/xqq/mpegts.js) 1.8.2 (Apache-2.0), embedded in the binary.

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
