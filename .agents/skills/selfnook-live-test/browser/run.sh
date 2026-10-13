#!/bin/sh
# run.sh FILE|- [SCREENSHOT]: sends browser code to the test browser, prints
# its JSON answer, and copies the page's screenshot to SCREENSHOT
# (default: ./shot.png).
code=$(cat "${1:--}")
printf '%s' "$code" | docker exec -i selfnook-test-dind sh -c 'curl -s --max-time 300 --data-binary @- http://$(docker inspect selfnook-test-browser --format "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"):9000/run'
echo
docker cp -q selfnook-test-dind:/srv/pw/shot.png "${2:-shot.png}" 2>/dev/null
