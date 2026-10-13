#!/bin/sh
# terminal.sh SESSION [TEXT|-] [WAIT]: types TEXT and Enter into a tmux
# session inside the test container (- types nothing), waits WAIT seconds
# (default 2), then prints the end of the screen.
s=$1; shift
if [ "$#" -gt 0 ] && [ "$1" != "-" ]; then
  docker exec selfnook-test-dind tmux send-keys -t "$s" -l -- "$1"
  docker exec selfnook-test-dind tmux send-keys -t "$s" Enter
fi
sleep "${2:-2}"
docker exec selfnook-test-dind tmux capture-pane -p -J -S -60 -t "$s" | sed '/^$/d' | tail -45
