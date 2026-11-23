#!/bin/bash
# PR #70 head db85c79 (bc03eb2 + rejected dispatch projected as failed). Only credit-demo Go code
# changed, so only the credit image is built: w05-db85/credit:dev from db85c79 as is, and
# w05-db85/credit:revoked from the same source with exactly one caller hold removed from the
# built-in credit-dd package (tool credit.apply / execute); in that copy only, Dockerfile.credit
# skips `go test` (scenario_golden_test pins the caller_holds digest). Orchestrator and
# professional images are the bc03eb2 ones (w05-bc03/*), unchanged by PR #70. Log ~/w05-db85-build.log.
set -e
log=~/w05-db85-build.log
if [ ! -d ~/w05-db85 ]; then
  git clone -q ~/w05-bc03 ~/w05-db85
  git -C ~/w05-db85 fetch -q /mnt/d/agent-hub-alpha/_deploy175/w05-db85.bundle pr70-db85c79
  git -C ~/w05-db85 checkout -q --detach db85c79fb96a51304ca3d423565852a0bcd849bf
fi
[ "$(git -C ~/w05-db85 rev-parse HEAD)" = db85c79fb96a51304ca3d423565852a0bcd849bf ]
cache=~/.cache/agent-hub-alpha/issue16
p=http://host.docker.internal:7890
proxy="--build-arg HTTP_PROXY=$p --build-arg HTTPS_PROXY=$p --build-arg http_proxy=$p --build-arg https_proxy=$p --build-arg NO_PROXY=localhost,127.0.0.1 --build-arg no_proxy=localhost,127.0.0.1"
ctx=~/w05-db85/agent-festival
echo "== $(date -Is) source $(git -C ~/w05-db85 rev-parse HEAD)" >> $log
docker build --progress plain $proxy -f $ctx/go/cmd/credit-demo/deploy/Dockerfile.credit --build-context tools=$cache/tools -t w05-db85/credit:dev $ctx >> $log 2>&1
echo "== $(date -Is) built credit:dev $(docker image inspect --format '{{.Id}}' w05-db85/credit:dev)" >> $log
dst=~/w05-db85-revoked-$(date +%Y%m%dT%H%M%S)
mkdir -p "$dst"
git -C ~/w05-db85 archive db85c79fb96a51304ca3d423565852a0bcd849bf agent-festival | tar -x -C "$dst"
f="$dst/agent-festival/go/cmd/credit-demo/scenarios/credit-dd.json"
python3 - "$f" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p, encoding="utf-8"))
before = len(doc["caller_holds"])
doc["caller_holds"] = [h for h in doc["caller_holds"] if not (h["kind"] == "tool" and h["selector"] == "credit.apply")]
assert before - len(doc["caller_holds"]) == 1, "expected exactly one credit.apply hold"
open(p, "w", encoding="utf-8").write(json.dumps(doc, ensure_ascii=False, indent=2) + "\n")
print("caller_holds", before, "->", len(doc["caller_holds"]))
PY
d="$dst/agent-festival/go/cmd/credit-demo/deploy/Dockerfile.credit"
grep -q 'go test ./... -count=1 && ' "$d"
sed -i 's#go test ./... -count=1 \&\& ##' "$d"
! grep -q 'go test' "$d"
echo "== $(date -Is) build credit:revoked from $dst" >> $log
docker build --progress plain $proxy -f "$d" --build-context tools=$cache/tools -t w05-db85/credit:revoked $dst/agent-festival >> $log 2>&1
echo "== $(date -Is) built credit:revoked $(docker image inspect --format '{{.Id}}' w05-db85/credit:revoked)" >> $log
for t in dev revoked; do echo "w05-db85/credit:$t $(docker image inspect --format '{{.Id}}' w05-db85/credit:$t)"; done
