# ps5cast

Affiche le flux de ta PS5 dans une fenêtre sur ton PC (Windows, macOS ou Linux), à partager sur Discord.
Pas de carte d'acquisition, pas de Remote Play, pas de réglage sur le routeur.

## Comment ça marche

La PS5 croit diffuser sur Twitch :
1. tu mets l'IP du PC comme **DNS primaire** de la PS5 ;
2. ps5cast répond aux requêtes DNS : les domaines d'ingest Twitch pointent vers le PC, le reste est relayé vers 1.1.1.1 ;
3. la PS5 envoie son flux RTMP (1080p60 H.264) au PC sur le port 1935 ;
4. ps5cast le diffuse, sans réencodage, vers :
   - **la page web intégrée** `http://localhost:8080` (Chrome/Edge, environ 200 ms de tampon, rien à installer) ;
   - **mpv** ou ffplay s'il est installé (fenêtre « PS5 », latence minimale) ;
   - **OBS / VLC** via `http://<ip-du-pc>:8080/live.flv`.

Rien n'est envoyé à Twitch. La clé de stream n'est jamais affichée ni enregistrée.

## Téléchargement

Prends le fichier de ton système dans la [dernière release](https://github.com/AlanSnt/ps5-cast/releases/latest) :

| Système | Fichier |
|---|---|
| Windows | [`ps5cast-windows-amd64.exe`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-windows-amd64.exe) |
| macOS Apple Silicon (M1 et suivants) | [`ps5cast-darwin-arm64.tar.gz`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-darwin-arm64.tar.gz) |
| macOS Intel | [`ps5cast-darwin-amd64.tar.gz`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-darwin-amd64.tar.gz) |
| Linux x86-64 | [`ps5cast-linux-amd64.tar.gz`](https://github.com/AlanSnt/ps5-cast/releases/latest/download/ps5cast-linux-amd64.tar.gz) |

Les binaires ne sont pas signés : Windows et macOS affichent un avertissement au premier lancement.

## Installation

1. *(Facultatif)* Installe mpv : `winget install mpv` (Windows), `brew install mpv` (macOS) ou le paquet `mpv` de ta distribution (Linux). Tu peux aussi déposer le binaire mpv à côté de ps5cast. Sans mpv, la page web s'ouvre toute seule.
2. Lance ps5cast :
   - **Windows** : double-clique sur `ps5cast-windows-amd64.exe`. Si SmartScreen bloque : Informations complémentaires → Exécuter quand même. Quand Windows le demande, autorise l'accès sur le **réseau privé**.
   - **macOS** : dans un terminal, depuis le dossier du téléchargement :
     ```sh
     tar -xzf ps5cast-darwin-arm64.tar.gz    # inutile si Safari a déjà extrait l'archive
     xattr -d com.apple.quarantine ps5cast   # lève le blocage Gatekeeper
     ./ps5cast
     ```
     Sur un Mac Intel, remplace `arm64` par `amd64`.
     Si le pare-feu macOS le demande, autorise les connexions entrantes.
   - **Linux** : le port 53 est réservé à root, donne ce droit au binaire une fois :
     ```sh
     tar -xzf ps5cast-linux-amd64.tar.gz
     sudo setcap cap_net_bind_service=+ep ./ps5cast
     ./ps5cast
     ```
3. Sur la PS5 : Paramètres → Réseau → Paramètres → Configurer la connexion Internet → ta connexion → Paramètres avancés
   - DNS : **Manuel**
   - DNS primaire : l'IP affichée par ps5cast
   - DNS secondaire : `1.1.1.1` (Internet continue de marcher quand le PC est éteint)
4. Il faut un compte Twitch lié à la PS5. Lance une diffusion Twitch avec le bouton Créer.
5. La page (ou la fenêtre mpv) affiche le jeu. Dans Discord : Partager l'écran → fenêtre du navigateur ou « PS5 ».

Page web : clic = activer le son (le navigateur démarre en muet), **f** plein écran, **m** muet, **s** stats de latence.
Console : **Entrée** rouvre la fenêtre mpv, **Ctrl+C** quitte.

### OBS / VLC
- OBS : Source → **Source média** → décocher « Fichier local » → Entrée : `http://<ip-du-pc>:8080/live.flv`.
- VLC : Média → Ouvrir un flux réseau → la même URL.

## Options

| Option | Défaut | Rôle |
|---|---|---|
| `-ip` | auto | IP locale du PC |
| `-upstream` | `1.1.1.1:53` | DNS public pour les autres requêtes |
| `-spoof` | `*.contribute.live-video.net,live*.twitch.tv` | domaines redirigés vers le PC |
| `-player` | `auto` | `mpv`, `ffplay` ou `none` |
| `-http` | `:8080` | page web et flux HTTP-FLV (vide = désactivé) |
| `-open` | `auto` | ouvre la page au démarrage : `auto` (si pas de mpv), `always`, `never` |
| `-record` | — | enregistre aussi le flux dans un fichier `.flv` |
| `-v` | off | affiche toutes les requêtes DNS |

## Dépannage

- **« port 53 déjà utilisé »** : un autre service occupe le port. Coupe-le, ou lance ps5cast sur une autre machine.
  - Windows : le partage de connexion, Docker ou Hyper-V.
  - Linux : souvent `systemd-resolved`. Mets `DNSStubListener=no` dans `/etc/systemd/resolved.conf`, puis :
    ```sh
    sudo ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf   # sans ça, le PC perd son propre DNS
    sudo systemctl restart systemd-resolved
    ```
- **Linux, « permission denied » sur le port 53** : la commande `setcap` de l'installation n'a pas été lancée (à refaire après chaque mise à jour du binaire).
- **La PS5 diffuse mais rien n'arrive** : regarde les lignes `dns …` dans la console. Si un nouveau domaine d'ingest apparaît, ajoute-le avec `-spoof "*.contribute.live-video.net,live*.twitch.tv,nouveau.domaine"`.
- **Message « tente une connexion TLS sur :443 »** : la PS5 essaie RTMPS (chiffré), qu'on ne peut pas intercepter. Le domaine loggé juste avant ne doit pas être redirigé.
- **Rien du tout** : vérifie le pare-feu du PC (ports 53 UDP/TCP et 1935 TCP entrants ; profil privé sous Windows).

## Build

```sh
go build -trimpath -ldflags="-s -w" .
```

Pour une autre plateforme, ajoute `GOOS` et `GOARCH` (par exemple `GOOS=windows GOARCH=amd64`).

### Publier une release

Pousse un tag `vX.Y.Z` : le workflow [`build.yml`](.github/workflows/build.yml) compile les quatre binaires et les attache à une release GitHub. Le tag devient la version affichée au démarrage.

```sh
git tag v0.2.0
git push origin v0.2.0
```

Dépendances : `github.com/miekg/dns` et `github.com/yutopp/go-rtmp`. Le lecteur web utilise [mpegts.js](https://github.com/xqq/mpegts.js) 1.8.2 (Apache-2.0), intégré au binaire.
