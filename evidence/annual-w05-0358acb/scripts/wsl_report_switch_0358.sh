#!/bin/bash
# Switch the WSL annual-chain deployment to the frozen receipt baseline
# 0358acb (#16 decision). Only docs, plugin flow view, a verify script and a
# Java test differ from 8c31bcd; src/main, deploy scripts and the execution
# profile inputs are identical, so jars/binaries stay and only the plugin is
# rebuilt and reinstalled. Log ~/report-start.log.
set -e
cd ~/report-demo-env/repo
repo=$PWD
log=~/report-start.log
step() { echo "== $(date -Is) $*" >> $log; }
run() { step "RUN $*"; "$@" >> $log 2>&1 || { step "FAILED $*"; exit 1; }; }
test -z "$(git diff 0358acb HEAD --name-only -- 'demos/report-demo/*/src/main/*' demos/report-demo/deploy demos/report-demo/control-go demos/report-demo/workswarm/configure.py demos/report-demo/workswarm/install_policy.py demos/report-demo/scenes)" || { step "unexpected runtime diff vs 0358acb"; exit 1; }
run git checkout --detach 0358acbad0ce1b0ce73772f31eda845057ba4dc5
step "repo now $(git rev-parse HEAD)"
export PATH=$repo/.local/tools/node/bin:/usr/local/bin:/usr/bin:/bin
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
run npm --prefix demos/report-demo/workswarm/workbench-plugin/app run check
run npm --prefix demos/report-demo/workswarm/workbench-plugin/app run build
step "plugin rebuilt at 0358acb"
bash /mnt/d/agent-hub-alpha/_deploy175/wsl_report_ws_restart.sh
step "artifact digests at 0358acb:"
sha256sum demos/report-demo/orchestrator-java/target/report-orchestrator.jar demos/report-demo/experts-java/target/report-experts.jar .local/report-demo/bin/report-control >> $log
(cd demos/report-demo/workswarm/workbench-plugin && find frontend/dist -type f | sort | xargs sha256sum | sha256sum | sed 's/-$/plugin frontend\/dist (tree)/') >> $log
