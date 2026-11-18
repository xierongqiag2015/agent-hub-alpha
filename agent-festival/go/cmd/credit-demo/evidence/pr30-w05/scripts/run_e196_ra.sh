#!/bin/bash
# R-A related variant on candidate e1961fe8 (project agent-hub-e196, credit on
# 127.0.0.1:5391): related installed with its model calls in flight, another
# node UNKNOWN, immediate resume through /api/resume. The 41a3675 D2 project is
# not touched. Output in ~/e196-ra/<ts>/.
cd ~/w05-e196/agent-festival/go/cmd/credit-demo/deploy || exit 1
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
export CREDIT_DEMO_PASSWORD="$(grep '^CREDIT_DEMO_PASSWORD=' ~/.config/aha-swarm/secrets.env | cut -d= -f2-)"
P=agent-hub-e196
export COMPOSE_PROJECT_NAME=$P CREDIT_PORT=5391 BUS_TEST_BASE=http://127.0.0.1:5391 BUS_TEST_PROJECT=$P
scripts=/mnt/d/agent-hub-alpha/w05ev/agent-festival/go/cmd/credit-demo/evidence/pr30-w05/scripts
export W05_FAULT_PROXY=$scripts/w05_fault_proxy.py
dir=~/e196-ra/$(date +%Y%m%dT%H%M%S); mkdir -p $dir
log=$dir/run.log
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "== $(date -Is) source $(git -C ~/w05-e196 rev-parse HEAD)" >> $log
for i in orchestrator professional credit; do docker image inspect --format "w05-e196/$i:dev {{.Id}}" w05-e196/$i:dev >> $log; done
python3 - >> $log 2>&1 <<PY
import demo
demo.BUS = True
demo.compose('-f', demo.HERE/'compose.bus-test.yaml', '-f', '$scripts/compose.w05-send-test.yaml', '-f', '/mnt/d/agent-hub-alpha/_deploy175/compose.w05-e196.yaml',
             'up', '-d', '--no-build', '--wait', 'credit', 'bus-db', 'rdc', 'gateway', 'material-agent', 'analysis-agent', 'writer-agent', 'orchestrator', 'fault-proxy')
demo.wait_bus()
PY
echo "== $(date -Is) up exit $?" >> $log
sleep 35
python3 /mnt/d/agent-hub-alpha/_deploy175/e196_ra_watch.py $dir/scenario.out http://127.0.0.1:5391 $P > $dir/watch.jsonl 2>&1 &
watch=$!
timeout 1500 python3 $scripts/w05_rd_extra.py ra-completed-then-unknown --resume external > $dir/scenario.out 2>&1
echo "== $(date -Is) scenario exit $?" >> $log
wait $watch
for c in orchestrator credit fault-proxy material-agent analysis-agent writer-agent gateway; do docker logs --since $since $P-$c-1 > $dir/$c.log 2>&1; done
docker cp $P-fault-proxy-1:/tmp/bus-fault-events.jsonl $dir/fault-events.jsonl >> $log 2>&1
cp $(grep -o '/home/[^ ]*\.json' $dir/scenario.out | tail -1) $dir/ 2>/dev/null
echo "== $(date -Is) collected; stopping $P" >> $log
bash /mnt/d/agent-hub-alpha/_deploy175/wsl_stop_project.sh $P >> $log 2>&1
echo "== $(date -Is) done $dir" >> $log
echo $dir
