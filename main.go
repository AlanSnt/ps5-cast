// ps5cast — capture le broadcast Twitch de la PS5 sur le réseau local et
// l'affiche dans une fenêtre (mpv/ffplay) à partager sur Discord.
//
// Principe : la PS5 utilise ce PC comme DNS primaire. Les domaines d'ingest
// Twitch sont résolus vers l'IP du PC, tout le reste est relayé vers un DNS
// public. Le flux RTMP reçu sur :1935 est remuxé en FLV et envoyé au lecteur.
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

// version est remplacée par le tag git lors d'un build de release (-X main.version).
var version = "dev"

func main() {
	var (
		ipFlag    = flag.String("ip", "", "IP locale du PC (auto-détectée si vide)")
		upstream  = flag.String("upstream", "1.1.1.1:53", "DNS public vers lequel relayer les autres requêtes")
		spoofFlag = flag.String("spoof", strings.Join(defaultSpoof, ","), "motifs de domaines à rediriger vers ce PC (séparés par des virgules)")
		player    = flag.String("player", "auto", "lecteur : auto | mpv | ffplay | none")
		record    = flag.String("record", "", "enregistre aussi le flux dans ce fichier .flv")
		verbose   = flag.Bool("v", false, "affiche toutes les requêtes DNS")
		probe443  = flag.Bool("probe443", true, "écoute :443 pour détecter une tentative RTMPS (non interceptable)")
		httpAddr  = flag.String("http", ":8080", "adresse de la page web / du flux HTTP-FLV (vide = désactivé)")
		openFlag  = flag.String("open", "auto", "ouvrir la page dans le navigateur : auto (si pas de mpv/ffplay) | always | never")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	ip := net.ParseIP(*ipFlag)
	if ip == nil {
		var err error
		if ip, err = detectLocalIP(); err != nil {
			log.Fatalf("impossible de détecter l'IP locale, passe-la avec -ip : %v", err)
		}
	}
	ip = ip.To4()
	if ip == nil {
		log.Fatal("-ip doit être une adresse IPv4")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sink := NewSink(*player, *record)
	defer sink.Close()

	dnsSrv, err := StartDNS(ip, *upstream, splitPatterns(*spoofFlag), *verbose)
	if err != nil {
		log.Fatalf("DNS : %v\n→ le port 53 est peut-être déjà utilisé (partage de connexion Windows, Docker, Hyper-V…)", err)
	}
	defer dnsSrv.Shutdown()

	rtmpSrv, err := StartRTMP(":1935", sink)
	if err != nil {
		log.Fatalf("RTMP : %v", err)
	}
	defer rtmpSrv.Close()

	if *probe443 {
		startTLSProbe(ctx)
	}

	pageURL := ""
	if *httpAddr != "" {
		httpSrv, err := StartHTTP(*httpAddr, sink)
		if err != nil {
			log.Fatalf("HTTP : %v (change le port avec -http :8081)", err)
		}
		defer httpSrv.Close()
		pageURL = "http://localhost" + portOf(*httpAddr)
	}

	printBanner(ip, *httpAddr, hasLocalPlayer(*player))

	if pageURL != "" && (*openFlag == "always" || (*openFlag == "auto" && !hasLocalPlayer(*player))) {
		openBrowser(pageURL)
	}

	// Entrée = rouvrir le lecteur s'il a été fermé.
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			sink.Reopen()
		}
	}()

	<-ctx.Done()
	log.Println("arrêt…")
}

func printBanner(ip net.IP, httpAddr string, localPlayer bool) {
	view := "  La fenêtre \"PS5\" (mpv) s'ouvre : partage-la dans Discord.\n"
	if !localPlayer {
		view = "  (mpv non trouvé : lecture dans le navigateur)\n"
	}
	if httpAddr != "" {
		p := portOf(httpAddr)
		view += fmt.Sprintf("  Navigateur : http://localhost%s  (ou http://%s%s depuis un autre appareil)\n", p, ip, p)
		view += fmt.Sprintf("  OBS / VLC  : http://%s%s/live.flv\n", ip, p)
	}
	fmt.Printf(`
  ps5cast %s — prêt
  ─────────────────────────────────────────────────────────
  Sur la PS5 : Paramètres → Réseau → Paramètres → Configurer
  la connexion Internet → (ta connexion) → Paramètres avancés
     Paramètres DNS : Manuel
     DNS primaire   : %s
     DNS secondaire : 1.1.1.1   (pour garder Internet sans ce PC)

  Puis lance une diffusion Twitch depuis la PS5 (bouton Créer).
%s
  Entrée = rouvrir la fenêtre mpv · Ctrl+C = quitter
  ─────────────────────────────────────────────────────────

`, version, ip, view)
}

// detectLocalIP renvoie l'IP de l'interface utilisée pour sortir sur Internet.
// Aucun paquet n'est envoyé : un "dial" UDP choisit juste la route.
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

// startTLSProbe écoute :443 uniquement pour diagnostiquer : si la PS5 tente
// RTMPS sur un domaine redirigé, on le signale clairement puis on coupe.
func startTLSProbe(ctx context.Context) {
	l, err := net.Listen("tcp4", ":443")
	if err != nil {
		return // 443 pris par autre chose : pas grave, c'est du diagnostic
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
				log.Printf("⚠ %s tente une connexion TLS sur :443 (RTMPS ?) — non interceptable. "+
					"Regarde les domaines DNS loggés juste avant et retire-le de -spoof.", c.RemoteAddr())
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
