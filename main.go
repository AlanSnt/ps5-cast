// ps5cast captures the PS5's Twitch broadcast on the local network and shows
// it in a window (mpv/ffplay) to share on Discord.
//
// How it works: the PS5 uses this PC as its primary DNS. Twitch ingest domains
// resolve to the PC's IP, everything else is relayed to a public DNS. The RTMP
// stream received on :1935 is remuxed to FLV and sent to the player.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// version is replaced by the git tag in release builds (-X main.version).
var version = "dev"

func main() {
	var (
		ipFlag    = flag.String("ip", "", "local IP of this PC (auto-detected if empty)")
		upstream  = flag.String("upstream", "1.1.1.1:53", "public DNS that other queries are relayed to")
		spoofFlag = flag.String("spoof", "", "exact domain patterns redirected to this PC, comma-separated (default: "+strings.Join(defaultSpoof, ",")+" plus the ingest servers Twitch publishes)")
		player    = flag.String("player", "auto", "player: auto | mpv | ffplay | none")
		record    = flag.String("record", "", "also record the stream to this .flv file (never overwrites: adds -2, -3…)")
		verbose   = flag.Bool("v", false, "log every DNS query")
		probe443  = flag.Bool("probe443", true, "listen on :443 to detect an RTMPS attempt (cannot be intercepted)")
		httpAddr  = flag.String("http", ":8080", "address of the web page and HTTP-FLV stream (empty = disabled)")
		openFlag  = flag.String("open", "auto", "open the page in the browser: auto (if no mpv/ffplay) | always | never")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	ip, err := localIP(*ipFlag)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sink := NewSink(*player, *record)
	defer sink.Close()

	dnsSrv, err := StartDNS(ip, *upstream, spoofPatterns(ctx, *spoofFlag, twitchIngestsURL), *verbose)
	if err != nil {
		log.Fatalf("DNS: %v\n→ port 53 is already in use (Windows connection sharing, Docker, Hyper-V, systemd-resolved…) or not permitted (Linux: setcap, see the README)", err)
	}
	defer dnsSrv.Shutdown()

	rtmpSrv, _, err := StartRTMP(":1935", sink)
	if err != nil {
		log.Fatalf("RTMP: %v", err)
	}
	defer rtmpSrv.Close()

	if *probe443 {
		startTLSProbe(ctx)
	}

	var localURL, lanURL string
	if *httpAddr != "" {
		httpSrv, bound, err := StartHTTP(*httpAddr, sink)
		if err != nil {
			log.Fatalf("HTTP: %v (change the port with -http :8081)", err)
		}
		defer httpSrv.Close()
		localURL, lanURL = httpURLs(bound, ip)
	}

	playerName := localPlayerName(*player)
	fmt.Print(banner(ip, playerName, localURL, lanURL))
	go announceNewerVersion(ctx, version)

	if localURL != "" && (*openFlag == "always" || (*openFlag == "auto" && playerName == "")) {
		openBrowser(localURL)
	}

	// Enter reopens the player if it was closed.
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			sink.Reopen()
		}
	}()

	<-ctx.Done()
	log.Println("shutting down…")
}

// banner is the startup message. playerName is "" without a local player,
// localURL is "" when the web page is disabled, lanURL is "" on loopback only.
func banner(ip net.IP, playerName, localURL, lanURL string) string {
	var view, keys string
	switch {
	case playerName != "":
		view = fmt.Sprintf("  The \"PS5\" window (%s) opens: share it in Discord.\n", playerName)
		keys = fmt.Sprintf("Enter = reopen the %s window · ", playerName)
	case localURL != "":
		view = "  No local player: the stream plays in the browser.\n"
	default:
		view = "  ⚠ No local player and no web page: nothing displays the stream.\n"
	}
	if localURL != "" {
		view += "  Browser   : " + localURL
		if lanURL != "" && lanURL != localURL {
			view += fmt.Sprintf("  (or %s from another device)", lanURL)
		}
		streamURL := lanURL
		if streamURL == "" {
			streamURL = localURL
		}
		view += fmt.Sprintf("\n  OBS / VLC : %s/live.flv\n", streamURL)
	}
	return fmt.Sprintf(`
  ps5cast %s — ready
  ─────────────────────────────────────────────────────────
  On the PS5: Settings → Network → Settings → Set Up Internet
  Connection → (your connection) → Advanced Settings
     DNS Settings  : Manual
     Primary DNS   : %s
     Secondary DNS : 1.1.1.1   (keeps Internet working without this PC)

  Then start a Twitch broadcast from the PS5 (Create button).
%s
  %sCtrl+C = quit
  ─────────────────────────────────────────────────────────

`, version, ip, view, keys)
}

// localIP returns the IPv4 address the PS5 must use as DNS: the -ip flag when
// it is set, otherwise the address of the interface that reaches the Internet.
func localIP(flagValue string) (net.IP, error) {
	if flagValue == "" {
		ip, err := detectLocalIP()
		if err != nil {
			return nil, fmt.Errorf("cannot detect the local IP, pass it with -ip: %w", err)
		}
		return ip.To4(), nil
	}
	ip := net.ParseIP(flagValue).To4()
	if ip == nil {
		return nil, fmt.Errorf("-ip %q is not an IPv4 address", flagValue)
	}
	return ip, nil
}

// httpURLs returns the page URL for this PC and the one for other devices on
// the LAN, which is empty when the server only listens on loopback.
func httpURLs(bound *net.TCPAddr, lanIP net.IP) (local, lan string) {
	port := strconv.Itoa(bound.Port)
	switch {
	case len(bound.IP) == 0 || bound.IP.IsUnspecified():
		return "http://localhost:" + port, "http://" + net.JoinHostPort(lanIP.String(), port)
	case bound.IP.IsLoopback():
		return "http://" + net.JoinHostPort(bound.IP.String(), port), ""
	default:
		url := "http://" + net.JoinHostPort(bound.IP.String(), port)
		return url, url
	}
}

// detectLocalIP returns the IP of the interface used to reach the Internet.
// No packet is sent: a UDP "dial" only picks the route.
func detectLocalIP() (net.IP, error) {
	c, err := net.Dial("udp4", "1.1.1.1:80")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP, nil
}

func splitPatterns(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, strings.TrimSuffix(p, "."))
		}
	}
	return out
}

// startTLSProbe listens on :443 for diagnosis only: if the PS5 attempts RTMPS
// on a redirected domain, say so clearly and drop the connection.
func startTLSProbe(ctx context.Context) {
	l, err := net.Listen("tcp4", ":443")
	if err != nil {
		return // 443 taken by something else: fine, this is only a diagnostic
	}
	go func() { <-ctx.Done(); l.Close() }()
	go func() {
		var last time.Time
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			if time.Since(last) > 10*time.Second {
				log.Printf("⚠ %s attempts a TLS connection on :443 (RTMPS?), which cannot be intercepted. "+
					"Check the DNS domains logged just before and pass -spoof with the redirected domains except that one.", c.RemoteAddr())
				last = time.Now()
			}
			c.Close()
		}
	}()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}
