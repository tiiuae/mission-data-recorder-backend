#!/bin/sh
session="work"
tmux start-server
tmux new-session -s $session -d -x "$(tput cols)" -y "$(tput lines)"
tmux set-option -g default-shell /bin/bash

tmux select-pane -t 0
tmux split-window -v -p 75
# 0
# 1
tmux select-pane -t 0
tmux split-window -h -p 66
tmux select-pane -t 1
tmux split-window -h
# 0 1 2
# 3
tmux select-pane -t 3
tmux split-window -v
# 0 1 2
# 3
# 4
tmux select-pane -t 3
tmux split-window -h
tmux select-pane -t 5
tmux split-window -h
# 0 1 2
# 3 4
# 5 6

# new window for emulator
tmux new-window
tmux send-keys "cd ../../fleet-management/e2e-drone-emulator" C-m
tmux send-keys "npx ts-node f4f-emulator d1 d2 $1" C-m

tmux select-window -t 0

tmux select-pane -t 0
tmux send-keys "cd ../../fleet-management/web-backend" C-m
tmux send-keys "go run . 127.0.0.1:8883" C-m

tmux select-pane -t 1
tmux send-keys "cd ../../dronsole-containers/mission-control" C-m
tmux send-keys "rm -rf repositories/ && go run . 127.0.0.1:2222 127.0.0.1:8883" C-m

tmux select-pane -t 3
tmux send-keys "cd ../../communication_link/communicationlink" C-m
tmux send-keys "go run . -device_id d1 -mqtt_broker 127.0.0.1:8883 -private_key rsa_private.pem" C-m

tmux select-pane -t 4
tmux send-keys "cd ../../communication_link/missionengine/cmd" C-m
tmux send-keys "go run . -device_id d1" C-m

tmux select-pane -t 5
tmux send-keys "cd ../../communication_link/communicationlink" C-m
tmux send-keys "go run . -device_id d2 -mqtt_broker 127.0.0.1:8883 -private_key rsa_private.pem" C-m

tmux select-pane -t 6
tmux send-keys "cd ../../communication_link/missionengine/cmd" C-m
tmux send-keys "go run . -device_id d2" C-m

tmux select-pane -t 2
tmux send-keys "alias q='tmux kill-session'" C-m

tmux attach-session -t $session