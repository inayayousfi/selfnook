# Selfnook

A local Docker Compose stack for Jellyfin, qBittorrent, Prowlarr, Sonarr, Radarr, Recyclarr, and a Homepage dashboard, served through one address by the Caddy reverse proxy.

Every media-aware container sees the same `/media` path. This avoids remote path mappings and allows hardlinks between downloads and libraries.

The setup supports Windows with Docker Desktop and native Linux.

## What it installs

| Service | Purpose | Address |
| --- | --- | --- |
| Homepage | Links, container status, VPN status and Docker resource overview | `ADDRESS/` |
| Caddy | Reverse proxy, HTTPS, and the single entry point for every web page | No web interface |
| Authentik | Admin sign-in for the administration pages, and the place to add sign-in methods such as Google accounts | `ADDRESS:9091` |
| Docker socket proxy | Read-only container status for Homepage | No web interface |
| Gluetun or Tailscale | Selectable qBittorrent VPN gateway | No web interface |
| Jellyfin | Media server | `ADDRESS/jellyfin` |
| Seerr | Movie and series requests sent to Radarr and Sonarr | `ADDRESS:5055`, also opened by `ADDRESS/seerr` |
| VPN country | Gluetun country selection, linked from Homepage's VPN card | `ADDRESS/vpn-country/` |
| qBittorrent | Download client | `ADDRESS/qbittorrent/` |
| Prowlarr | Indexer manager | `ADDRESS/prowlarr` |
| Sonarr | Series manager | `ADDRESS/sonarr` |
| Radarr | Movie manager | `ADDRESS/radarr` |
| Recyclarr | Quality profile synchronization | No web interface |

`ADDRESS` depends on the access mode chosen during setup:

| Access mode | Address | Reachable from |
| --- | --- | --- |
| Tailscale HTTPS certificate | `https://MACHINE.TAILNET.ts.net` | Devices signed in to the same Tailscale network |
| Own domain with a Let's Encrypt certificate | `https://media.example.com` | The whole internet, through router port forwarding |
| Local network name without encryption | `http://MACHINE.local` | The local network, unencrypted |

Tailscale mode requires that Tailscale Serve does not already use HTTPS port `443` on the computer; setup and `start` stop with the removal command when it does.

Caddy publishes ports `80`, `443`, `5055` (Seerr), and `9091` (sign-in). The other web interfaces no longer publish their own ports. In own-domain mode, forward TCP ports `80`, `443`, `5055`, and `9091` from the router to this computer.

One admin username and password protect qBittorrent, Sonarr, Radarr, Prowlarr, and the VPN country page. Those applications no longer ask for their own logins: Sonarr, Radarr, and Prowlarr use their `External` authentication method, and qBittorrent skips its login for the stack's Docker network (`172.31.250.0/24`). Jellyfin and Seerr stay outside the admin sign-in, because Jellyfin's phone and TV applications cannot pass it and Seerr is meant for the people who request media. Jellyfin's own sign-in page has a `Sign in with Authentik` button: every Authentik user can sign in with it, and members of the Authentik group `authentik Admins` become Jellyfin administrators. Seerr keeps its own login.

Authentik checks the admin sign-in. Its database runs in its own PostgreSQL container and is stored in the Docker volume `selfnook_authentik-db`, not under `config/`, because PostgreSQL cannot keep its files in a folder shared with Windows. Back up that volume together with `config/`. Other sign-in methods are added in Authentik's administration pages at `ADDRESS:9091/if/admin/`: Google and other accounts under Directory > Federation and Social login. In local mode the address is unencrypted, so passkeys and Google sign-in cannot work there; setup and Authentik's sign-in page both say so.

Containers keep reaching each other through `sonarr:8989`, `radarr:7878`, `prowlarr:9696`, and `jellyfin:8096`. Those names belong to Caddy, which adds each application's path only when the address lacks it. Saved addresses in Prowlarr, Seerr, Recyclarr, and the Jellyfin notifications therefore need no path.

The qBittorrent traffic port `6881` remains exposed for incoming torrent connections. Jellyfin's discovery port `7359/udp` remains open, and Jellyfin announces `ADDRESS/jellyfin` to the applications that discover it.

qBittorrent and Prowlarr have no independent container network connection. They share the selected VPN gateway, so torrent traffic and indexer searches leave through the VPN and bypass internet-provider DNS blocking. Sonarr and Radarr continue to use `qbittorrent` and `prowlarr` as host names. `qbittorrent` points to the active gateway's shared network location, and `prowlarr` points to Caddy, which forwards to that location. When the VPN is down, Prowlarr is unreachable, so Sonarr and Radarr cannot search.

Setup offers NordVPN, Proton VPN, Surfshark, Private Internet Access, a Tailscale exit node, and another Gluetun OpenVPN provider. The Gluetun choices use OpenVPN UDP and no country filter by default. Gluetun selects from all matching provider servers.

NordVPN requires its generated service username and password from:

```text
Nord Account > NordVPN > Advanced Settings > Set up NordVPN manually > Service credentials
```

Proton VPN uses its separate OpenVPN username and password. Surfshark uses credentials generated under `VPN > Manual setup > Desktop or mobile > OpenVPN > Credentials`. Private Internet Access uses the assigned service username beginning with `p` and its service password. The provider-specific guide appears before either credential prompt.

The Tailscale choice requests a one-off, non-ephemeral auth key and the exact name or Tailscale IP of an existing exit node. Setup starts every other service first, then waits with no time limit until the VPN route is ready before starting qBittorrent and Prowlarr. The same applies to Gluetun. If the Tailscale gateway later restarts, qBittorrent remains without networking, but the stack may need to be restarted to reconnect qBittorrent to the replacement network namespace.

The selected gateway and its required secrets are stored in the saved settings. Gluetun's firewall provides the kill switch for its providers. The Tailscale path starts qBittorrent only after the fixed exit node reports online and fails closed if its shared gateway network disappears.

NordVPN does not provide inbound port forwarding. Downloads still work, but incoming peer connectivity and seeding can be weaker than with a provider that supports a forwarded torrent port.

Homepage provides one page with links to every web interface and the running state of each container. Jellyfin and Seerr appear first; the administration interfaces and the VPN status are in a folded Admin group. It reads that state through a socket proxy that allows only read requests about containers, so Homepage cannot start, stop or create containers. With Gluetun, a VPN panel shows the public address and country through Gluetun's control server. Setup generates the key for that panel and allows it to read only the public address. The VPN country page lists the provider's OpenVPN countries from the installed Gluetun, reconnects Gluetun to the selected country without restarting qBittorrent or Prowlarr, and saves the choice in `config/vpn-country`. `start` copies that choice into the saved settings before starting Gluetun, and the page reapplies it if Gluetun restarts with another country. The page has its own Gluetun key, limited to reading the public address and reading or changing VPN settings, and it cannot read the saved settings. The admin sign-in protects the page. Its Glances widget reports CPU and memory use from Docker's Linux environment, not the complete Windows host. Its media-disk figure reports the capacity and free space of the filesystem containing the selected media directory, not only the size of files inside that directory. Network usage is omitted because accurate Docker-environment network totals require broader container permissions.

## Media layout

The setup asks for one media directory and creates this structure:

```text
Media/
|-- Downloads/
|-- Movies/
`-- Series/
```

Every container uses the following paths:

```text
/media/Downloads
/media/Movies
/media/Series
```

Application state is stored in the `config/` directory next to the program.

## Requirements

Install these tools before running the setup:

- Docker Engine with Docker Compose 2.20 or newer on Linux
- Docker Desktop using Linux containers on Windows

Docker must be running before setup begins.

On Windows, Docker Desktop must have access to the selected media drive.

## Download

Download the program for your computer from the latest release:

https://github.com/inayayousfi/selfnook/releases/latest

| Computer | File | Save it as |
| --- | --- | --- |
| Windows | `selfnook-windows-amd64.exe` | `selfnook.exe` |
| Linux on Intel or AMD | `selfnook-linux-amd64` | `selfnook` |
| Linux on ARM, such as a Raspberry Pi | `selfnook-linux-arm64` | `selfnook` |

Save it in an empty folder at its permanent location. The program keeps `config/` and the generated Docker Compose files in its own folder, and automatic startup uses that absolute path, so moving the folder later will break startup.

The saved settings, including every password and API key, are kept in one file readable only by your user account:

| System | File |
| --- | --- |
| Linux | `~/.local/state/selfnook/state.json`, or `$XDG_STATE_HOME/selfnook/state.json` when that variable is set |
| Windows | `%LOCALAPPDATA%\selfnook\state.json` |
| macOS | `~/Library/Application Support/selfnook/state.json` |

An earlier version kept these values in `.env` in the program folder. The first command run with this version moves `.env` and `config/setup-state.json` into the state file, then deletes both.

On Linux, allow the program to run:

```bash
chmod +x selfnook
```

## First setup

Open a terminal inside that folder.

On Windows:

```powershell
.\selfnook.exe setup
```

On Linux:

```bash
./selfnook setup
```

The program performs these checks and actions:

1. Verifies that Docker and Docker Compose are available.
2. Asks for the media directory.
3. Offers the supported VPN gateways, then presents a `Y/n` confirmation for the qBittorrent legal notice.
4. Shows the selected gateway's credential instructions and requests only that gateway's required values.
5. Shows the pros and cons of the 3 access modes, then requests the mode and its address. Tailscale mode uses this computer's Tailscale name. Own-domain mode requests the domain name. Local mode proposes `MACHINE.local` and accepts another name containing a dot.
6. Requests the admin username and password that protect the administration pages.
7. Creates the media and configuration directories.
8. Writes the saved settings and Authentik's configuration with the admin sign-in. In Tailscale mode on Linux, it runs `sudo tailscale set --operator=USER` once, so later starts can renew the certificate without a password, then fetches the certificate.
9. Writes Caddy's configuration, Jellyfin's `/jellyfin` base URL, and qBittorrent's login bypass for the stack's Docker network, and installs Jellyfin's sign-in plugin, version 5.1.1 of `K0lin/jellyfin-plugin-sso`, after checking its checksum.
10. Starts the selected VPN gateway, Caddy, Authentik and its database, the Docker socket proxy, Homepage, Glances, Jellyfin, Sonarr, Radarr, and Seerr, then starts qBittorrent and Prowlarr once the VPN route is ready.
11. Shows each application's address and setup steps, starting with the admin sign-in values, and saves progress after each manual step.
12. Requests the Sonarr and Radarr API keys.
13. Sets up Jellyfin without questions: creates the administrator `selfnook` with a generated password, the Movies and Shows libraries, the API key, and the Authentik sign-in, then shows the steps that make Radarr and Sonarr refresh Jellyfin after each import.
14. Applies the Recyclarr profiles.
15. Shows the Seerr connection steps with the saved Jellyfin login and the Sonarr and Radarr API keys.
16. Installs automatic startup.

Run the program without a command to open a full-screen interface instead. It offers the same actions as the commands below, with the same guides and questions.

Setup reuses values already saved, including credentials and API keys. Existing application configuration under `config/` remains available.

An interrupted setup keeps the saved settings and its completed steps in the state file. Run the same setup command again to continue. Docker Compose operations and automatic-start installation are safe to repeat.

Leading and trailing spaces are removed from the media-directory input. Password prompts display `*` characters instead of showing the password itself or leaving the input visually blank.

## Manual application setup

The program prints these instructions during setup.

The first administration page asks for the admin sign-in. One sign-in covers every administration page.

### qBittorrent

Open:

```text
ADDRESS/qbittorrent/
```

qBittorrent skips its own login for the stack, so no temporary password is needed.

1. Open `Tools > Options > Downloads`.
2. Set `Saving Management > Default Save Path` to `/media/Downloads`.
3. Set `Keep incomplete torrents in` to `/media/Downloads/incomplete`.
4. Click `Save`.

### Sonarr

Open:

```text
ADDRESS/sonarr
```

1. Open `Settings > Media Management`.
2. Under `Root Folders`, click `Add Root Folder`.
3. Click `Ok` after entering `/media/Series`.
4. Open `Settings > Download Clients`.
5. Click `Add`, then select qBittorrent.
6. Set `Host` to `qbittorrent` and `Port` to `8080`. Leave `API Key`, `Username`, and `Password` empty.
7. Set `Category` to `sonarr`.
8. Click `Test`, then `Save`.

Use `qbittorrent`, not `localhost`. Inside the Sonarr container, `localhost` means Sonarr itself.

Sonarr creates its API key automatically. Copy it for the next terminal prompt:

```text
Settings > General > Security > API Key
```

### Radarr

Open:

```text
ADDRESS/radarr
```

1. Open `Settings > Media Management`.
2. Under `Root Folders`, click `Add Root Folder`.
3. Click `Ok` after entering `/media/Movies`.
4. Open `Settings > Download Clients`.
5. Click `Add`, then select qBittorrent.
6. Set `Host` to `qbittorrent` and `Port` to `8080`. Leave `API Key`, `Username`, and `Password` empty.
7. Set `Category` to `radarr`.
8. Click `Test`, then `Save`.

Radarr creates its API key automatically. Copy it from:

```text
Settings > General > Security > API Key
```

### Prowlarr

Open:

```text
ADDRESS/prowlarr
```

1. Open `Settings > Apps`.
2. Add Sonarr with `Full Sync`.
3. Set `Prowlarr Server` to `http://prowlarr:9696`.
4. Set `Sonarr Server` to `http://sonarr:8989`.
5. Use the Sonarr API key copied during Sonarr setup, then test and save.
6. Add Radarr with `Full Sync`.
7. Set `Prowlarr Server` to `http://prowlarr:9696`.
8. Set `Radarr Server` to `http://radarr:7878`.
9. Use the Radarr API key copied during Radarr setup, then test and save.
10. Open `Indexers`, add your indexers, then test each one.

### Jellyfin

Setup configures Jellyfin itself. It creates the administrator `selfnook` with a generated password saved in the state file, the Movies library on `/media/Movies`, the Shows library on `/media/Series`, and an API key named `Selfnook`. It then points the sign-in plugin at Authentik and adds the `Sign in with Authentik` button to Jellyfin's sign-in page. Each `start` writes those sign-in settings again when they differ, so edits to the plugin's provider `authentik`, the sign-in page message, or the custom CSS are replaced.

A Jellyfin set up before Selfnook managed it already has an administrator whose login Selfnook does not know. Setup then stops and asks you to record that login with `./selfnook credentials jellyfin`.

Jellyfin applications on phones and TVs connect to `ADDRESS/jellyfin`.

### Jellyfin notifications

On Windows, Docker does not pass file-change notices from a Windows drive into containers, so Jellyfin's real-time monitoring does not see new files. Radarr and Sonarr refresh Jellyfin after each import instead:

1. In Radarr, open `Settings > Connect`, click `+`, then select `Emby / Jellyfin`.
2. Set `Name` to `Jellyfin`.
3. Check `On File Import`, `On File Upgrade`, `On Rename`, `On Movie Delete`, `On Movie File Delete`, and `On Movie File Delete For Upgrade`.
4. Set `Host` to `jellyfin`, `Port` to `8096`, and `API Key` to the Jellyfin API key.
5. Keep `Update Library` checked, then click `Test` and `Save`.
6. Repeat in Sonarr, checking `On File Import`, `On File Upgrade`, `On Import Complete`, `On Rename`, `On Series Delete`, `On Episode File Delete`, and `On Episode File Delete For Upgrade`.

Files added to the media folders by hand still appear only after Jellyfin's scheduled library scan, which runs every 12 hours by default.

### Seerr

Open:

```text
ADDRESS:5055
```

1. Choose Jellyfin as the server type, with `jellyfin` as the Jellyfin URL and `8096` as the port.
2. Enter an email address of your choice and the Jellyfin administrator username and password that setup shows, then click `Sign In`.
3. Click `Sync Libraries`, enable the Movies and Shows libraries, then click `Continue`.
4. Add a Radarr server marked `Default Server`: host `radarr`, port `7878`, the Radarr API key, quality profile `4K Progressive`, root folder `/media/Movies`.
5. Add a Sonarr server marked `Default Server`: host `sonarr`, port `8989`, the Sonarr API key, quality profile `4K Progressive`, root folder `/media/Series`, with `Season Folders` checked.
6. Click `Finish Setup`.

Seerr signs people in with a Jellyfin username and password. People whose Jellyfin account was created through `Sign in with Authentik` have no Jellyfin password, so they cannot sign in to Seerr.

## Recyclarr profiles

Recyclarr creates one profile named `4K Progressive` in Sonarr and Radarr.

The Sonarr profile uses the TRaSH Guides WEB 2160p combined profile. It accepts a lower available quality and upgrades to 4K later.

The Radarr profile uses the TRaSH Guides UHD Bluray and WEB rules. It prefers these qualities in order:

```text
Bluray 2160p
WEB 2160p
Bluray 1080p
WEB 1080p
HDTV 1080p
Bluray 720p
WEB 720p
HDTV 720p
DVD
SDTV
```

The profile upgrades downloaded movies until Bluray 2160p is available.

Both profiles favor HDR10+ while retaining HDR10 compatibility. Dolby Vision releases without an HDR fallback are rejected.

Anime uses the same progressive profiles.

The stack expects subtitles to be embedded in the media files. It does not install Bazarr or download missing subtitles.

Recyclarr runs after setup and after every `selfnook start`. Docker Compose commands typed by hand do not receive the saved settings, so start and stop the stack with the program.

## Daily commands

Show every command with a short description:

```bash
./selfnook help
```

`-h` prints the same general help. Add `-h` after any command for its detailed help:

```bash
./selfnook setup -h
./selfnook start -h
./selfnook stop -h
./selfnook update -h
./selfnook status -h
./selfnook vpn-status -h
./selfnook add-user -h
./selfnook credentials -h
```

Start the stack and synchronize Recyclarr:

```bash
./selfnook start
```

On Windows, replace `./selfnook` with `.\selfnook.exe`.

Show container status:

```bash
./selfnook status
```

Check the selected VPN gateway (Gluetun health or Tailscale exit-node availability):

```bash
./selfnook vpn-status
```

Create a person's account in every app that keeps its own login, which is Seerr, since Seerr cannot sign people in through Authentik:

```bash
./selfnook add-user
```

It asks for the person's email address and name, then shows the Seerr address, the email, and a generated password. Selfnook does not save that password, so give it to the person right away.

List saved groups, or display one group (including any saved password or API key) and change its credentials:

```bash
./selfnook credentials
./selfnook credentials admin
./selfnook credentials jellyfin
./selfnook credentials vpn
```

After the values are shown, answer `y` to replace credentials one by one; press Enter to keep a saved value. Answer `n` or press Enter to leave everything unchanged. For Jellyfin and the VPN, this updates the saved settings only, so change the password in the application as well. The admin username and password, VPN credentials, and the Sonarr and Radarr API keys take effect at the next `start`; the admin sign-in needs no other change. The Jellyfin values belong to the administrator account, which can create other Jellyfin users and reset their passwords in Jellyfin's Dashboard. The values appear in the terminal, so use this on a private screen.

Stop the stack:

```bash
./selfnook stop
```

## Automatic startup

### Linux user service

The setup can install:

```text
~/.config/systemd/user/selfnook.service
~/.config/systemd/user/selfnook-check.service
~/.config/systemd/user/selfnook-check.timer
```

It starts with the user's systemd session. The timer runs `selfnook check` 5 minutes after boot and every 15 minutes; it restarts Caddy when a port Caddy publishes stops answering. Its output is written to `config/check.log` only when it acts.

Inspect it with:

```bash
systemctl --user status selfnook.service
```

Disable and remove it with:

```bash
systemctl --user disable --now selfnook-check.timer selfnook.service
rm ~/.config/systemd/user/selfnook.service ~/.config/systemd/user/selfnook-check.service ~/.config/systemd/user/selfnook-check.timer
systemctl --user daemon-reload
```

### Linux system service

The setup can instead install:

```text
/etc/systemd/system/selfnook.service
/etc/systemd/system/selfnook-check.service
/etc/systemd/system/selfnook-check.timer
```

This option requires `sudo` and starts the stack during system boot. The timer runs the same check as the user service.

Inspect it with:

```bash
sudo systemctl status selfnook.service
```

Disable and remove it with:

```bash
sudo systemctl disable --now selfnook-check.timer selfnook.service
sudo rm /etc/systemd/system/selfnook.service /etc/systemd/system/selfnook-check.service /etc/systemd/system/selfnook-check.timer
sudo systemctl daemon-reload
```

### Windows

The setup creates a hidden launcher named `selfnook.vbs` in the current user's Startup folder.

The launcher waits up to five minutes for Docker Desktop. Startup output is written to:

```text
config/startup.log
```

It also schedules the task `Selfnook check`, which runs the hidden launcher `selfnook-check.vbs` in the program's folder every 15 minutes while the user is signed in. The check restarts Caddy when a port Caddy publishes stops answering, and writes to `config/check.log` only when it acts.

Remove the launcher from:

```text
%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup
```

Remove the scheduled check with:

```bat
schtasks /Delete /TN "Selfnook check" /F
del selfnook-check.vbs
```

## Updating

Container images are pinned by multi-platform digest in each application's Compose file under `internal/apps/`. The program carries those files and writes them to `compose.yaml` and `compose/` in its folder on every command, so edits to the written copies are overwritten. A new installation therefore uses the same image builds until a release with updated digests replaces the program.

After replacing the program with a newer release, `start` keeps each existing container on the image it runs and lists every container whose version differs from the release. A container changed with another tool is kept the same way. Choose which containers to replace:

```bash
./selfnook update
```

`update` asks about each listed container, deletes the ones you approve, and creates them again from the pinned images. `stop` keeps the containers, so they start as they were.

Application data remains under `config/`. Jellyfin 12.2, Sonarr 4.0.20, and Radarr 6.4.4 upgrade their databases when they first start, and an older version cannot read the upgraded database. Back up `config/` before approving those replacements.

## Backups

Back up these items:

```text
the state file listed under Download
config/
Media/
```

The state file contains the admin sign-in, Authentik's generated secrets, the Jellyfin administrator login, the secret Jellyfin shares with Authentik, the VPN credentials, and the Sonarr, Radarr, and Jellyfin API keys. The stack uses all of them directly. Do not commit or share this file.

The Recyclarr configuration, `internal/apps/recyclarr/recyclarr.yml`, contains no secrets and is part of the program. The program fills in the addresses of Sonarr and Radarr when it writes the file.

## Previous stack

This repository previously used Docker named volumes. The new setup does not migrate them.

Existing named volumes remain untouched, but the new containers do not mount them. Recover or migrate their contents manually before deleting those volumes.

An older `.env` also remains accessible in the public Git history. Credentials from the previous stack must be treated as exposed and replaced. The new setup generates fresh application configuration and collects the new Sonarr and Radarr keys.

## Security notes

qBittorrent is routed through the selected gateway. Gluetun requires `NET_ADMIN`; the Tailscale gateway requires `NET_ADMIN` and `NET_RAW`. Both require access to `/dev/net/tun`. No other application service shares that gateway network.

Every web page goes through Caddy. The admin sign-in protects qBittorrent, Sonarr, Radarr, Prowlarr, and the VPN country page, which do not ask for their own logins. Containers on the stack's Docker network reach those applications without signing in, so do not attach other containers to that network. Jellyfin and Seerr are outside the admin sign-in. Jellyfin accepts its generated administrator login and any Authentik account; Seerr accepts Jellyfin usernames and passwords.

In own-domain mode, every page is reachable from the internet. In local mode, passwords and pages cross the local network unencrypted.

In Tailscale mode on Linux, setup makes the current user Tailscale's operator, so that user can change Tailscale settings without `sudo`. `start` renews the 90-day certificate; the certificate expires if `start` does not run for that long.

Jellyfin does not officially support Docker on Windows or macOS. This does not mean it cannot work, so the setup remains worth trying. Some features, particularly hardware-accelerated transcoding, may still fail on those hosts.
