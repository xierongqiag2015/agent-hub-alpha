#!/bin/bash
# Build a test-only credit image from bc03eb2 whose built-in credit-dd package lacks exactly one
# caller hold: tool credit.apply / execute (the revocation in the #30 item-2 comparison).
# The source is exported to a new timestamped directory; ~/w05-bc03 stays at bc03eb2.
# In that copy only, Dockerfile.credit skips `go test` (scenario_golden_test pins the package's
# caller_holds digest, which this revocation changes); go vet and the build are kept.
# Tag w05-bc03/credit:revoked. Log ~/w05-bc03-build.log.
set -e
log=~/w05-bc03-build.log
src=~/w05-bc03
dst=~/w05-bc03-revoked-$(date +%Y%m%dT%H%M%S)
mkdir -p "$dst"
git -C $src archive bc03eb2315ef6b32b4f5df9ac55a0c393967c77a agent-festival | tar -x -C "$dst"
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
echo "source $dst; Dockerfile.credit without go test: $(grep -c 'go vet' "$d") vet line(s)"
cache=~/.cache/agent-hub-alpha/issue16
p=http://host.docker.internal:7890
echo "== $(date -Is) build credit:revoked from $dst" >> $log
docker build --progress plain --build-arg HTTP_PROXY=$p --build-arg HTTPS_PROXY=$p --build-arg http_proxy=$p --build-arg https_proxy=$p \
  --build-arg NO_PROXY=localhost,127.0.0.1 --build-arg no_proxy=localhost,127.0.0.1 \
  -f "$d" --build-context tools=$cache/tools -t w05-bc03/credit:revoked $dst/agent-festival >> $log 2>&1
echo "== $(date -Is) built credit:revoked $(docker image inspect --format '{{.Id}}' w05-bc03/credit:revoked)" >> $log
docker image inspect --format '{{.Id}}' w05-bc03/credit:revoked
