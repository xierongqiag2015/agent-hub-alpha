#!/bin/bash
# Restart the report compiler after worker-enable when services.py cannot
# match its own record (WSL btime shift). Ownership is checked first: the PID
# must be the recorded one, run report-orchestrator.jar from this repo and
# hold 127.0.0.1:8195. Log ~/report-start.log.
set -e
cd ~/report-demo-env/repo
repo=$PWD
private=$repo/.local/report-demo
log=~/report-start.log
pid=$(python3 -I -c "import json,sys;print(json.load(open(sys.argv[1]))['compiler']['pid'])" $private/services.json)
cmd=$(tr '\0' ' ' < /proc/$pid/cmdline)
cwd=$(readlink /proc/$pid/cwd)
port=$(ss -ltnp | grep ":8195 " | grep -o "pid=[0-9]*" | cut -d= -f2)
echo "== $(date -Is) compiler ownership: pid=$pid cwd=$cwd port_pid=$port cmd=$cmd" >> $log
[[ "$cmd" == *"$repo/demos/report-demo/orchestrator-java/target/report-orchestrator.jar"* && "$cwd" == "$repo" && "$port" == "$pid" ]] || { echo "ownership check failed" >> $log; exit 1; }
kill -TERM $pid
for i in $(seq 1 20); do kill -0 $pid 2>/dev/null || break; sleep 0.5; done
kill -0 $pid 2>/dev/null && { echo "compiler did not exit" >> $log; exit 1; }
echo "== $(date -Is) compiler $pid stopped" >> $log
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
export PATH=$JAVA_HOME/bin:/usr/local/bin:/usr/bin:/bin
export REPORT_DEMO_CONFIG=$private/config.json
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
$repo/.local/runtime/workswarm-venv/bin/python demos/report-demo/deploy/services.py start compiler --private-root "$private" >> $log 2>&1
echo "== $(date -Is) compiler restarted with worker enabled" >> $log
