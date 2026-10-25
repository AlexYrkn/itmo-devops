#!/bin/sh
set -e

cd "$(dirname "$0")"

CGROUP="/sys/fs/cgroup/mydocker-api-$(id -u)"
USER_ID=$(id -u)
GROUP_ID=$(id -g)

printf '+memory +cpu +pids\n' | sudo tee /sys/fs/cgroup/cgroup.subtree_control >/dev/null
sudo mkdir -p "$CGROUP"
printf '67108864\n' | sudo tee "$CGROUP/memory.max" >/dev/null
printf '0\n' | sudo tee "$CGROUP/memory.swap.max" >/dev/null
printf '50000 100000\n' | sudo tee "$CGROUP/cpu.max" >/dev/null
printf '32\n' | sudo tee "$CGROUP/pids.max" >/dev/null

sudo sh -ec '
    printf "%s\n" "$$" > "$1/cgroup.procs"
    exec setpriv --reuid "$2" --regid "$3" --clear-groups \
        unshare --user --map-root-user --pid --fork --kill-child=TERM \
        --mount --mount-proc --net --uts --ipc \
        sh -ec "hostname lab1; ip link set lo up; exec setpriv --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs ./seccomp-launcher -- ./api/main"
' sh "$CGROUP" "$USER_ID" "$GROUP_ID"
