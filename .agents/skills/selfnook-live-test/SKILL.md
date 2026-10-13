---
name: selfnook-live-test
description: Run Selfnook end to end inside a disposable Docker-in-Docker container, with a real VPN, a browser driven by code, legal films, and a local test indexer, then request a film in Seerr and follow it until Jellyfin lists it. Use when asked to test Selfnook live, to verify a change against running apps, or to reproduce a problem without touching the user's computer setup.
---

# Selfnook live test

The test runs the real `selfnook` program and real app images inside one privileged container named `selfnook-test-dind`. That container has its own Docker engine, so nothing reaches the computer's own containers, ports, or startup files. The program's own Docker commands run inside it unchanged.

Keep private values out of every printed line: the VPN login, the admin password, and API keys. Pass them through shell variables read from files with mode 600 in the session's scratch folder, and filter them out of any screen capture you print.

## Helpers in this folder

- `terminal.sh SESSION [TEXT|-] [WAIT]` types into a tmux session inside the test container and prints the end of its screen. Selfnook's interactive commands run in tmux there.
- `browser/server.js` keeps one Chromium page open inside the stack network and runs JavaScript posted to `:9000/run`. Sign-in cookies survive between posts.
- `browser/run.sh FILE|- [SCREENSHOT]` posts code to that browser, prints the JSON answer, and copies a screenshot.
- `indexer/server.py` is a Torznab indexer (the search format Prowlarr reads) that lists one release, Sintel 1080p.
- `indexer/make_torrent.py` builds a single-file torrent whose web seed (an HTTP address that a torrent client downloads from like a peer) points at a local web server.

## 1. Build the test container

```sh
docker run -d --name selfnook-test-dind --privileged --device /dev/net/tun \
  -v selfnook-test-dind:/var/lib/docker docker:dind
docker exec selfnook-test-dind sh -c 'apk add --no-cache curl tmux bash && mkdir -p /srv/selfnook /srv/pw /srv/torznab'
CGO_ENABLED=0 GOOS=linux go build -o "$SCRATCH/selfnook" ./cmd/selfnook
docker cp "$SCRATCH/selfnook" selfnook-test-dind:/srv/selfnook/selfnook
```

`/dev/net/tun` lets Gluetun open its VPN tunnel. The volume holds about 10 GB of images and media.

## 2. VPN login

Gluetun needs a working VPN login, and the test cannot invent one. Ask the user where to take it from. One source the user has approved before is the NordVPN service login of the Windows computer's existing install, read without changing anything:

```sh
umask 077
ssh ziede@internet-gyal-terminal 'Select-String -Path C:\Users\ziede\media-stack\.env -Pattern "^(VPN_OPENVPN_USER|VPN_OPENVPN_PASSWORD)=" | ForEach-Object { $_.Line }' \
  | tr -d '\r' > "$SCRATCH/nord.env"
```

Print only the key names and value lengths to confirm the read. Type the values into the setup prompts with `tmux send-keys -l -- "$VPN_OPENVPN_USER"`, never through `terminal.sh`, which would print them.

## 3. Legal media

Both films are Creative Commons releases of the Blender Foundation.

- Big Buck Bunny, placed by hand to test Jellyfin's library scan:

  ```sh
  docker exec selfnook-test-dind sh -c 'mkdir -p "/srv/Media/Movies/Big Buck Bunny (2008)" && cd "/srv/Media/Movies/Big Buck Bunny (2008)" \
    && curl -sSfL -o b.zip https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_320x180.mp4.zip \
    && unzip -q b.zip && rm b.zip && mv BigBuckBunny_320x180.mp4 "Big Buck Bunny (2008).mp4"'
  ```

- Sintel 1080p, the film the request chain downloads. Its MD5 checksum is `377aa3357df96a9ae085169a74d2a3bf`.

  ```sh
  docker exec selfnook-test-dind sh -c 'cd /srv/torznab && curl -sSfL -o Sintel.2010.1080p.mkv https://download.blender.org/demo/movies/Sintel.2010.1080p.mkv && md5sum Sintel.2010.1080p.mkv'
  ```

The download goes through the test container's normal connection, not the VPN.

## 4. Run setup

```sh
docker exec selfnook-test-dind sh -c 'cd /srv/selfnook && tmux new-session -d -s setup -x 200 -y 50 "./selfnook setup; echo EXIT=\$?; sleep 100000"'
```

Answer with `terminal.sh setup <answer>`: media folder `/srv/Media`, the VPN provider, the legal notice, the VPN login (see step 2), access mode 3 (local network name without encryption) with the name `selfnook-test.local`, and the admin login. Keep the admin password in a mode 600 file.

Containers inside the stack reach `selfnook-test.local` because Selfnook gives Caddy that name on the stack network.

## 5. Start the browser, the indexer, and the web seed

Once setup has created the `selfnook` network:

```sh
docker cp browser/server.js selfnook-test-dind:/srv/pw/server.js
docker cp indexer/server.py selfnook-test-dind:/srv/torznab/server.py
docker cp indexer/make_torrent.py selfnook-test-dind:/srv/torznab/make_torrent.py
docker exec selfnook-test-dind sh -c '
  docker run -d --name selfnook-test-browser --network selfnook --ipc host -e NODE_PATH=/work/node_modules -v /srv/pw:/out -w /work \
    mcr.microsoft.com/playwright:v1.58.0-noble sh -c "npm init -y >/dev/null && npm i playwright@1.58.0 >/dev/null 2>&1 && node /out/server.js"
  docker run -d --name selfnook-test-seed --network selfnook -v /srv/torznab:/usr/share/nginx/html:ro nginx:alpine
  docker run --rm -v /srv/torznab:/d python:3.13-alpine python /d/make_torrent.py /d/Sintel.2010.1080p.mkv Sintel.2010.1080p.mkv \
    http://selfnook-test-seed/Sintel.2010.1080p.mkv /d/sintel-1080p.torrent
  docker run -d --name selfnook-test-torznab --network selfnook -v /srv/torznab:/data:ro python:3.13-alpine python /data/server.py'
```

The Playwright package version must match the image version. In Prowlarr, add the indexer as "Generic Torznab" with the URL `http://selfnook-test-torznab`.

The local indexer and web seed replace public sources for three observed reasons:

- download.blender.org answers 403 to NordVPN addresses, and qBittorrent downloads through the VPN.
- Prowlarr's Internet Archive indexer fails with "The response ended prematurely" although the same address works with curl.
- The official Sintel torrent holds a 123 MB file, which Radarr rejects because the quality sizes Recyclarr sets require at least 12.5 MB per minute for 1080p.

## 6. Follow every guide step in the browser

Do each step the setup guide prints, in the browser, as a user would. That checks the guide's wording against the real pages. When a later run only needs to reach the next stage, the same settings can go through each app's API from inside the browser page, with `fetch` and the app's `X-Api-Key` header. Say which steps used the API.

Sign in once through `http://selfnook-test.local/qbittorrent/` before the first admin page. Setup waits for Authentik before it prints the first guide, because Authentik needs one to two minutes on a fresh install.

The screenshot can show a page that has already changed. When it disagrees with the expected state, read `await page.locator('body').ariaSnapshot()` instead.

## 7. Request chain

In Seerr, request Sintel. Then follow it through Radarr's queue, qBittorrent's torrent list, and the import into `/srv/Media/Movies/Sintel (2010)/`, until Jellyfin lists it and Seerr shows "Available".

## 8. Other commands

Run `status`, `vpn-status`, `credentials`, `stop`, `start`, `check`, and `update` from `/srv/selfnook`. To test `check`, block Caddy's port 80 inside the test container with an iptables DROP rule and confirm that `check` restarts Caddy. Remove the rule afterwards. Start the interface with no argument in a second tmux session to compare it with the commands.

## Known limits

- The test container has no systemd, so setup's last step, Linux automatic start, fails with "Automatic start needs systemd, which does not run on this computer." Everything before it is set up.
- Rootless Docker on the computer maps container user 1000 to another host user, so the test container must be the one running the stack, not the computer's rootless engine.
- After many reconnects in one day, NordVPN can refuse the same login with `AUTH_FAILED` for a while. `start` then waits for the VPN without a time limit and prints "The VPN provider refuses the saved VPN login." every minute. Check that the user's own VPN still runs before blaming the login.
- Stop containers with `selfnook stop`. A forced `docker kill` once left qBittorrent restarting in a loop.

## Cleanup

Ask the user before removing anything. The test leaves:

- the container `selfnook-test-dind` and the volume `selfnook-test-dind`;
- the `docker:dind` image on the computer;
- private files in the session's scratch folder: the VPN login, the admin password, and API keys.

```sh
docker rm -f selfnook-test-dind && docker volume rm selfnook-test-dind && docker image rm docker:dind
```
