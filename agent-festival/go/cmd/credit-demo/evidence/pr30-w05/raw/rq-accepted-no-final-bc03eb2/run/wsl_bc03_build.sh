#!/bin/bash
# Build the integration candidate bc03eb2 (#60 merged into 67fd5b05) images (credit, orchestrator with the
# 0.1.3-aha-drain1 SDK patch, professional) under w05-bc03/* tags, from a
# WSL-native clone, so the 41a3675 D2 images (agent-hub-issue16/*) stay
# untouched. Log ~/w05-bc03-build.log.
set -e
cd ~
log=~/w05-bc03-build.log
if [ ! -d ~/w05-bc03 ]; then
  git clone -q ~/w05-5fb1 ~/w05-bc03 && git -C ~/w05-bc03 fetch -q /mnt/d/agent-hub-alpha/_deploy175/w05-bc03.bundle cand-bc03eb2 && git -C ~/w05-bc03 checkout -q --detach bc03eb2315ef6b32b4f5df9ac55a0c393967c77a
fi
ctx=~/w05-bc03/agent-festival
cache=~/.cache/agent-hub-alpha/issue16
p=http://host.docker.internal:7890
proxy="--build-arg HTTP_PROXY=$p --build-arg HTTPS_PROXY=$p --build-arg http_proxy=$p --build-arg https_proxy=$p --build-arg NO_PROXY=localhost,127.0.0.1 --build-arg no_proxy=localhost,127.0.0.1"
echo "== $(date -Is) source $(git -C $ctx rev-parse HEAD)" >> $log
build() {
  name=$1; shift
  echo "== $(date -Is) build $name" >> $log
  if docker build --progress plain $proxy "$@" >> $log 2>&1; then
    echo "== $(date -Is) built $name $(docker image inspect --format '{{.Id}}' w05-bc03/$name:dev)" >> $log
  else
    echo "== $(date -Is) FAILED $name" >> $log; return 1
  fi
}
build orchestrator -f $ctx/go/cmd/credit-demo/deploy/Dockerfile.orchestrator --build-context solution-src=$cache/sources/agent-solution -t w05-bc03/orchestrator:dev $ctx
build professional -f $ctx/go/cmd/credit-demo/deploy/Dockerfile.agent -t w05-bc03/professional:dev $ctx
build credit -f $ctx/go/cmd/credit-demo/deploy/Dockerfile.credit --build-context tools=$cache/tools -t w05-bc03/credit:dev $ctx
echo "== $(date -Is) all built" >> $log
