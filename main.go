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
		spoofFlag = flag.String("spoof", strings.Join(defaultSpoof, ","), "domain patterns redirected to this PC (comma-separated)")
		player    = flag.String("player", "auto", "player: auto | mpv | ffplay | none")
		record    = flag.String("record", "", "also record the stream to this .flv file (never overwrites: adds -2, -3…)")
		verbose   = flag.Bool("v", false, "log every DNS query")
		probe443  = flag.Bool("probe443", true, "listen on :443 to detect an RTMPS attempt (cannot be intercepted)")
		httpAddr  = flag.String("http", ":8080", "address of the web page and HTTP-FLV stream (empty = disabled)")
		openFlag  = flag.String("open", "auto", "open the page in the browser: auto (if no mpv/ffplay) | always | never")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	ip := net.ParseIP(*ipFlag)
	if ip == nil {
		var err error
		if ip, err = detectLocalIP(); err != nil {
			log.Fatalf("cannot detect the local IP, pass it with -ip: %v", err)
		}
	}
	ip = ip.To4()
	if ip == nil {
		log.Fatal("-ip must be an IPv4 address")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sink := NewSink(*player, *record)
	defer sink.Close()

	dnsSrv, err := StartDNS(ip, *upstream, splitPatterns(*spoofFlag), *verbose)
	if err != nil {
		log.Fatalf("DNS: %v\n→ port 53 is already in use (Windows connection sharing, Docker, Hyper-V, systemd-resolved…) or not permitted (Linux: setcap, see the README)", err)
	}
	defer dnsSrv.Shutdown()

	rtmpSrv, err := StartRTMP(":1935", sink)
	if err != nil {
		log.Fatalf("RTMP: %v", err)
	}
	defer rtmpSrv.Close()

	if *probe443 {
		startTLSProbe(ctx)
	}

	pageURL := ""
	if *httpAddr != "" {
		httpSrv, err := StartHTTP(*httpAddr, sink)
		if err != nil {
			log.Fatalf("HTTP: %v (change the port with -http :8081)", err)
		}
		defer httpSrv.Close()
		pageURL = "http://localhost" + portOf(*httpAddr)
	}

	printBanner(ip, *httpAddr, hasLocalPlayer(*player))

	if pageURL != "" && (*openFlag == "always" || (*openFlag == "auto" && !hasLocalPlayer(*player))) {
		openBrowser(pageURL)
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

func printBanner(ip net.IP, httpAddr string, localPlayer bool) {
	view := "  The \"PS5\" window (mpv) opens: share it in Discord.\n"
	if !localPlayer {
		view = "  (mpv not found: playing in the browser)\n"
	}
	if httpAddr != "" {
		p := portOf(httpAddr)
		view += fmt.Sprintf("  Browser   : http://localhost%s  (or http://%s%s from another device)\n", p, ip, p)
		view += fmt.Sprintf("  OBS / VLC : http://%s%s/live.flv\n", ip, p)
	}
	fmt.Printf(`
  ps5cast %s — ready
  ─────────────────────────────────────────────────────────
  On the PS5: Settings → Network → Settings → Set Up Internet
  Connection → (your connection) → Advanced Settings
     DNS Settings  : Manual
     Primary DNS   : %s
     Secondary DNS : 1.1.1.1   (keeps Internet working without this PC)

  Then start a Twitch broadcast from the PS5 (Create button).
%s
  Enter = reopen the mpv window · Ctrl+C = quit
  ─────────────────────────────────────────────────────────

`, version, ip, view)
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
					"Check the DNS domains logged just before and remove that one from -spoof.", c.RemoteAddr())
				last = time.Now()
			}
			c.Close()
		}
	}()
}

func portOf(addr string) string {
	_, p, err := net.SplitHostPort(addr)
	if err != nil || p == "" {
		return ""
	}
	return ":" + p
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
