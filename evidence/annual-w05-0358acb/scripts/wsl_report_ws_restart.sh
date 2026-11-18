#!/bin/bash
# LOCAL-RUN section 9 remedy for "your-model-name": stop the three WorkSwarm
# processes (ownership checked: recorded PID, module in cmdline, cwd = repo),
# rerun the assistant setup, restart them with the model environment, and
# record the workspace config state (no values) before and after.
cd ~/report-demo-env/repo || exit 1
repo=$PWD
private=$repo/.local/report-demo
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
export PATH=$repo/.local/tools/node/bin:$JAVA_HOME/bin:/usr/local/bin:/usr/bin:/bin
export WORKSWARM_SOURCE=$repo/.local/sources/jiuwenswarm REPORT_DEMO_CONFIG=$private/config.json
export UV_PROJECT_ENVIRONMENT=$repo/.local/runtime/workswarm-venv
py=$UV_PROJECT_ENVIRONMENT/bin/python
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
log=~/report-start.log
h=$private/workswarm-home
state() {
  echo "== $(date -Is) workspace state ($1)" >> $log
  stat -c '   %w birth | %y mod | %n' $h/config/config.yaml $h/config/.env >> $log 2>&1
  echo "   setup_guide: $(grep -A1 '^setup_guide:' $h/config/config.yaml | tail -1 | tr -d ' ')  progressive_tool_enabled: $(grep '^progressive_tool_enabled' $h/config/config.yaml | cut -d: -f2)" >> $log
  echo "   .env model keys: $(grep -oE '^(API_BASE|API_KEY|MODEL_NAME|MODEL_PROVIDER|ENDPOINT_PROFILE)=' $h/config/.env | tr -d '=' | tr '\n' ' ')" >> $log
}
umask 077
state "before"
for svc in workswarm-web workswarm-gateway workswarm-agentserver; do
  pid=$(python3 -I -c "import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]]['pid'])" $private/services.json $svc)
  mod=$(echo $svc | sed 's/workswarm-//')
  cmd=$(tr '\0' ' ' < /proc/$pid/cmdline 2>/dev/null); cwd=$(readlink /proc/$pid/cwd)
  case "$svc" in
    workswarm-web) want="channels.web.app_web";; workswarm-gateway) want="app_gateway";; workswarm-agentserver) want="app_agentserver";;
  esac
  echo "== $(date -Is) $svc ownership: pid=$pid cwd=$cwd cmd=$cmd" >> $log
  [[ "$cmd" == *"$want"* && "$cwd" == "$repo" ]] || { echo "   ownership check failed; stop" >> $log; exit 1; }
  kids=$(pgrep -P $pid | tr '\n' ' ')
  kill -TERM $pid $kids 2>/dev/null
  for i in $(seq 1 30); do kill -0 $pid 2>/dev/null || break; sleep 0.5; done
  kill -0 $pid 2>/dev/null && { echo "   $svc did not exit" >> $log; exit 1; }
  echo "   stopped $svc (children: ${kids:-none})" >> $log
done
state "after stop"
export PYTHONPATH="$WORKSWARM_SOURCE"
$py demos/report-demo/workswarm/configure.py --config "$private/config.json" --workspace "$h" >> $log 2>&1 || { echo "configure failed" >> $log; exit 1; }
unset PYTHONPATH
state "after configure"
swarm=~/.config/aha-swarm/secrets.env
export DEMO_MODEL_BASE_URL="$(grep '^SWARM_API_BASE=' $swarm | cut -d= -f2-)"
export DEMO_MODEL_NAME="$(grep '^SWARM_MODEL_NAME=' $swarm | cut -d= -f2-)"
export DEMO_MODEL_ENDPOINT_PROFILE=openai
export DEMO_MODEL_API_KEY="$(grep '^SWARM_API_KEY=' $swarm | cut -d= -f2-)"
$py demos/report-demo/deploy/services.py start workswarm-agentserver workswarm-gateway workswarm-web --private-root "$private" >> $log 2>&1
rc=$?
unset DEMO_MODEL_API_KEY
state "after start (rc=$rc)"
sleep 40
state "40s after start"
