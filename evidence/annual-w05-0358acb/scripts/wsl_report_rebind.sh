#!/bin/bash
# Rebind the annual chain to another model on the same endpoint and key (only
# DEMO_MODEL_NAME changes; profile stays openai), back up the previous binding,
# restart every process that reads the binding (ownership-checked stops), and
# validate. Usage: wsl_report_rebind.sh <model-name>. Log ~/report-start.log.
MODEL=$1
[ -n "$MODEL" ] || { echo "model name required"; exit 1; }
cd ~/report-demo-env/repo || exit 1
repo=$PWD
private=$repo/.local/report-demo
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
export PATH=$repo/.local/tools/node/bin:$repo/.local/tools/go/bin:$JAVA_HOME/bin:/usr/local/bin:/usr/bin:/bin
export WORKSWARM_SOURCE=$repo/.local/sources/jiuwenswarm REPORT_DEMO_CONFIG=$private/config.json
export UV_PROJECT_ENVIRONMENT=$repo/.local/runtime/workswarm-venv
export KEYCLOAK_HOME=$repo/.local/tools/keycloak-26.8.2
py=$UV_PROJECT_ENVIRONMENT/bin/python
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
log=~/report-start.log
h=$private/workswarm-home
step() { echo "== $(date -Is) $*" >> $log; }
run() { step "RUN $*"; "$@" >> $log 2>&1 || { step "FAILED $*"; exit 1; }; }
binding() { python3 -I -c "import json,sys;c=json.load(open(sys.argv[1]));print(c.get('execution_profile'), c.get('model_binding_digest'))" $private/config.json; }
umask 077
step "rebind to model $MODEL (same endpoint and key, profile openai)"
bk=~/report-demo-env/binding-backup/$(date +%Y%m%dT%H%M%S); mkdir -p $bk
cp $private/config.json $bk/ && cp -r $private/experts $bk/ || { step "backup failed"; exit 1; }
step "previous binding $(binding) backed up to $bk"

swarm=~/.config/aha-swarm/secrets.env
export DEMO_MODEL_BASE_URL="$(grep '^SWARM_API_BASE=' $swarm | cut -d= -f2-)"
export DEMO_MODEL_NAME="$MODEL"
export DEMO_MODEL_ENDPOINT_PROFILE=openai
export DEMO_MODEL_API_KEY="$(grep '^SWARM_API_KEY=' $swarm | cut -d= -f2-)"

stop_owned() {
  local name=$1 marker=$2 port=$3
  local pid=$(python3 -I -c "import json,sys;print(json.load(open(sys.argv[1])).get(sys.argv[2],{}).get('pid',''))" $private/services.json $name)
  if [ -z "$pid" ] || [ ! -d /proc/$pid ]; then step "$name: no live recorded process"; return 0; fi
  local cmd=$(tr '\0' ' ' < /proc/$pid/cmdline) cwd=$(readlink /proc/$pid/cwd)
  if [[ "$cmd" != *"$marker"* || "$cwd" != "$repo" ]]; then step "$name: ownership check failed pid=$pid cwd=$cwd"; exit 1; fi
  local lp=$(ss -ltnp | grep ":$port " | grep -o "pid=[0-9]*" | head -1 | cut -d= -f2)
  if [ "$lp" != "$pid" ]; then step "$name: port $port held by $lp, not $pid"; exit 1; fi
  local kids=$(pgrep -P $pid | tr '\n' ' ')
  kill -TERM $pid $kids 2>/dev/null
  for i in $(seq 1 40); do kill -0 $pid 2>/dev/null || break; sleep 0.5; done
  if kill -0 $pid 2>/dev/null; then step "$name did not exit"; exit 1; fi
  step "stopped $name pid=$pid (port $port; children: ${kids:-none})"
}
stop_owned workswarm-web channels.web.app_web 5173
stop_owned workswarm-gateway agent_gateway.app_gateway 19000
stop_owned workswarm-agentserver app_agentserver 18092
stop_owned report-writer report-experts.jar 8097
stop_owned asset-quality-analyst report-experts.jar 8096
stop_owned business-analyst report-experts.jar 8094
stop_owned report-extractor report-experts.jar 8093
stop_owned compiler report-orchestrator.jar 8195
stop_owned control bin/report-control 8091

run $py demos/report-demo/deploy/bind_profile.py --private-root "$private"
step "new binding $(binding)"
run $py demos/report-demo/deploy/services.py start control compiler report-extractor business-analyst asset-quality-analyst report-writer --private-root "$private"
run $py demos/report-demo/deploy/services.py start workswarm-agentserver workswarm-gateway workswarm-web --private-root "$private"
sleep 40
ws_ok() { grep -A1 '^setup_guide:' $h/config/config.yaml | tail -1 | grep -q 'enabled: false' && ! grep -qE '^(API_BASE|API_KEY|MODEL_NAME)=' $h/config/.env; }
if ws_ok; then step "WorkSwarm workspace kept its setup (setup_guide false, no model stubs)"; else
  step "WorkSwarm workspace was reset; reconfiguring"
  stop_owned workswarm-web channels.web.app_web 5173
  stop_owned workswarm-gateway agent_gateway.app_gateway 19000
  stop_owned workswarm-agentserver app_agentserver 18092
  PYTHONPATH="$WORKSWARM_SOURCE" run $py demos/report-demo/workswarm/configure.py --config "$private/config.json" --workspace "$h"
  run $py demos/report-demo/deploy/services.py start workswarm-agentserver workswarm-gateway workswarm-web --private-root "$private"
  sleep 40
  ws_ok && step "WorkSwarm workspace setup now held" || { step "WorkSwarm workspace still reset"; exit 1; }
fi
unset DEMO_MODEL_API_KEY
run $py demos/report-demo/tests/validate_services.py --private-root "$private"
run $py demos/report-demo/tests/identity_probe.py --private-root "$private"
step "rebind to $MODEL done"
