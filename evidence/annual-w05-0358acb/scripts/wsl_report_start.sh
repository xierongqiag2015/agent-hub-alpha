#!/bin/bash
# Start yougq report-demo 8c31bcd on WSL following LINUX-RUN sections 2 and 4,
# up to validation (worker stays disabled). Secrets are read from private
# files into the environment of the commands that need them and unset after.
# No proxy at runtime. Log ~/report-start.log.
set -e
cd ~/report-demo-env/repo
repo=$PWD
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
export PATH=$repo/.local/tools/apache-maven-3.9.16/bin:$repo/.local/tools/node/bin:$repo/.local/tools/go/bin:$JAVA_HOME/bin:/usr/local/bin:/usr/bin:/bin
export private=$repo/.local/report-demo
export WORKSWARM_SOURCE=$repo/.local/sources/jiuwenswarm
export REPORT_DEMO_CONFIG=$private/config.json
export UV_PROJECT_ENVIRONMENT=$repo/.local/runtime/workswarm-venv
export py=$UV_PROJECT_ENVIRONMENT/bin/python
export KEYCLOAK_HOME=$repo/.local/tools/keycloak-26.8.2
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
sec=~/report-demo-env/secrets
swarm=~/.config/aha-swarm/secrets.env
log=~/report-start.log
step() { echo "== $(date -Is) $*" >> $log; }
run() { step "RUN $*"; "$@" >> $log 2>&1 || { step "FAILED $*"; exit 1; }; }
umask 077
step "start $(git rev-parse HEAD)"

export W04_APP_DSN="postgres://aha_control_app:$(cat $sec/aha_control_app)@127.0.0.1:55432/agent_hub_demo?sslmode=disable"
export W04_MIGRATOR_DSN="postgres://aha_control_migrator:$(cat $sec/aha_control_migrator)@127.0.0.1:55432/agent_hub_demo?sslmode=disable"
run $py demos/report-demo/deploy/prepare_local.py --private-root "$private"
unset W04_APP_DSN W04_MIGRATOR_DSN

run $py demos/report-demo/deploy/services.py start keycloak --private-root "$private"
run $py demos/report-demo/deploy/configure_identity.py --private-root "$private"

export RDC_DB_URL='jdbc:postgresql://127.0.0.1:55432/agent_rdc' RDC_DB_USER=agent_rdc
export RDC_DB_PASSWORD="$(cat $sec/agent_rdc)"
run $py demos/report-demo/deploy/services.py configure --private-root "$private"
unset RDC_DB_PASSWORD
run $py demos/report-demo/deploy/services.py start rdc gateway --private-root "$private"
run "$private/bin/report-control" -migrate

export DEMO_MODEL_BASE_URL="$(grep '^SWARM_API_BASE=' $swarm | cut -d= -f2-)"
export DEMO_MODEL_NAME="$(grep '^SWARM_MODEL_NAME=' $swarm | cut -d= -f2-)"
export DEMO_MODEL_ENDPOINT_PROFILE=openai
export DEMO_MODEL_API_KEY="$(grep '^SWARM_API_KEY=' $swarm | cut -d= -f2-)"
run $py demos/report-demo/deploy/bind_profile.py --private-root "$private"
run $py demos/report-demo/deploy/services.py start control compiler report-extractor business-analyst asset-quality-analyst report-writer --private-root "$private"
run $py demos/report-demo/deploy/configure_bus.py --private-root "$private"
run $py demos/report-demo/deploy/services.py start registry-heartbeat --private-root "$private"
export PYTHONPATH="$WORKSWARM_SOURCE"
run $py demos/report-demo/workswarm/configure.py --config "$private/config.json" --workspace "$private/workswarm-home"
run $py demos/report-demo/deploy/services.py start workswarm-agentserver workswarm-gateway workswarm-web --private-root "$private"
unset DEMO_MODEL_API_KEY

run $py demos/report-demo/tests/validate_services.py --private-root "$private"
run $py demos/report-demo/tests/identity_probe.py --private-root "$private"
step "started; worker still disabled"
run $py demos/report-demo/deploy/services.py status --private-root "$private"
