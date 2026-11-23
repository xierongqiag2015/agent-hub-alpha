#!/bin/bash
# rv-effect-revoke on PR #70 head db85c79 (project agent-hub-db85,
# credit on 127.0.0.1:5394), fault proxy in front of the real Gateway.
# Usage: wsl_db85_run_revoke.sh <scenario> [scenario args]. The 41a3675 D2 project is
# not touched. Output in ~/w05-db85-runs/<ts>-<scenario>/.
cd ~/w05-db85/agent-festival/go/cmd/credit-demo/deploy || exit 1
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
export CREDIT_DEMO_PASSWORD="$(grep '^CREDIT_DEMO_PASSWORD=' ~/.config/aha-swarm/secrets.env | cut -d= -f2-)"
P=agent-hub-db85
export COMPOSE_PROJECT_NAME=$P CREDIT_PORT=5394 BUS_TEST_BASE=http://127.0.0.1:5394 BUS_TEST_PROJECT=$P
scripts=/mnt/d/agent-hub-alpha/w05ev/agent-festival/go/cmd/credit-demo/evidence/pr30-w05/scripts
export W05_FAULT_PROXY=$scripts/w05_fault_proxy.py
export W05_COMPOSE_FILES=$PWD/compose.bus-test.yaml:$scripts/compose.w05-send-test.yaml:/mnt/d/agent-hub-alpha/_deploy175/compose.w05-db85.yaml
export W05_REVOKED_OVERLAY=/mnt/d/agent-hub-alpha/_deploy175/compose.w05-db85-revoked.yaml
scenario=$1; shift
dir=~/w05-db85-runs/$(date +%Y%m%dT%H%M%S)-$scenario; mkdir -p $dir
log=$dir/run.log
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "== $(date -Is) source $(git -C ~/w05-db85 rev-parse HEAD)" >> $log
for i in w05-bc03/orchestrator:dev w05-bc03/professional:dev w05-db85/credit:dev w05-db85/credit:revoked; do docker image inspect --format "$i {{.Id}}" $i >> $log; done
python3 - >> $log 2>&1 <<PY
import demo
demo.BUS = True
demo.compose('-f', demo.HERE/'compose.bus-test.yaml', '-f', '$scripts/compose.w05-send-test.yaml', '-f', '/mnt/d/agent-hub-alpha/_deploy175/compose.w05-db85.yaml',
             'up', '-d', '--no-build', '--wait', 'credit', 'bus-db', 'rdc', 'gateway', 'material-agent', 'analysis-agent', 'writer-agent', 'orchestrator', 'fault-proxy')
demo.wait_bus()
PY
echo "== $(date -Is) up exit $?" >> $log
sleep 35
timeout 3600 python3 ${W05_SCENARIO_SCRIPT:-$scripts/w05_rd_extra.py} $scenario "$@" > $dir/scenario.out 2>&1
echo "== $(date -Is) scenario exit $?" >> $log
for c in orchestrator credit fault-proxy material-agent analysis-agent writer-agent gateway; do docker logs --since $since $P-$c-1 > $dir/$c.log 2>&1; done
docker cp $P-fault-proxy-1:/tmp/bus-fault-events.jsonl $dir/fault-events.jsonl >> $log 2>&1
cp $(grep -o '/home/[^ ]*\.json' $dir/scenario.out | tail -1) $dir/ 2>/dev/null
echo "== $(date -Is) collected; stopping $P" >> $log
bash /mnt/d/agent-hub-alpha/_deploy175/wsl_stop_project.sh $P >> $log 2>&1
echo "== $(date -Is) done $dir" >> $log
echo $dir
