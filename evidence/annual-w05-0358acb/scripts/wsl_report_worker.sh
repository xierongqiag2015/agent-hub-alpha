#!/bin/bash
# LINUX-RUN section 4 tail: enable the worker and restart the compiler so it
# consumes approved tasks. No secrets needed. Log ~/report-start.log.
set -e
cd ~/report-demo-env/repo
repo=$PWD
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
export PATH=$repo/.local/tools/go/bin:$JAVA_HOME/bin:/usr/local/bin:/usr/bin:/bin
private=$repo/.local/report-demo
export REPORT_DEMO_CONFIG=$private/config.json
py=$repo/.local/runtime/workswarm-venv/bin/python
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
log=~/report-start.log
step() { echo "== $(date -Is) $*" >> $log; }
run() { step "RUN $*"; "$@" >> $log 2>&1 || { step "FAILED $*"; exit 1; }; }
run $py demos/report-demo/deploy/services.py worker-enable --private-root "$private"
run $py demos/report-demo/deploy/services.py stop compiler --private-root "$private"
run $py demos/report-demo/deploy/services.py start compiler --private-root "$private"
run $py demos/report-demo/deploy/services.py status --private-root "$private"
step "worker enabled; compiler restarted"
