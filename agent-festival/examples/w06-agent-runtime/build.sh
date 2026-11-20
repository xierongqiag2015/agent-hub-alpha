#!/bin/sh
# Build the W06 Bus runtime jar in a throwaway Maven container.
# Usage: build.sh <W06 source dir: .../W06-wanshoulu-1117/source/w06-annual-report-agent> <output dir>
# 1. copy the W06 source, apply the one-line adaptations (patches/w06-pr36-adaptations.patch), install the
#    W06 agents library into the container's Maven repository;
# 2. package this wrapper against it. Proxy settings come from MAVEN_SETTINGS (optional).
set -e
here=$(cd "$(dirname "$0")" && pwd)
w06=$(cd "$1" && pwd)
out=$(mkdir -p "$2" && cd "$2" && pwd)
settings=${MAVEN_SETTINGS:-}
docker run --rm -v "$w06":/w06:ro -v "$here":/runtime:ro -v "$out":/out ${settings:+-v "$settings":/settings.xml:ro} \
  -v w05-m2:/root/.m2 maven:3.9-eclipse-temurin-21 sh -c '
set -e
s=""; [ -f /settings.xml ] && s="-s /settings.xml"
cp -r /w06 /build-w06 && cp -r /runtime /build-runtime
f=/build-w06/agents/src/main/java/com/agenthub/w06/agents/material/MaterialAgentFactory.java
[ "$(grep -c "store.load();" $f)" = 0 ] || { echo "W06 source already loads the snapshot; drop the adaptation"; exit 2; }
sed -i "s/^\( *\)SnapshotStore store = new SnapshotStore(snapshotDir);/&\n\1store.load();/" $f
[ "$(grep -c "store.load();" $f)" = 1 ] || { echo "adaptation did not apply exactly once"; exit 3; }
m=/build-w06/agents/src/main/java/com/agenthub/w06/agents/metrics/MetricAgentFactory.java
[ "$(grep -c "亿元/% 等" $m)" = 1 ] || { echo "metrics prompt changed upstream; review the adaptation"; exit 4; }
sed -i "s|亿元/% 等|亿元/%% 等|" $m
[ "$(grep -c "亿元/%% 等" $m)" = 1 ] || { echo "metrics adaptation did not apply exactly once"; exit 5; }
for a in metrics/MetricAgentFactory operation/OperationAgentFactory assetquality/AssetQualityAgentFactory writer/WriterAgentFactory; do
  g=/build-w06/agents/src/main/java/com/agenthub/w06/agents/$a.java
  [ "$(grep -c "\.id(\"ask_user\")" $g)" = 1 ] || { echo "ask_user card changed upstream in $a; review the adaptation"; exit 6; }
  sed -i "s/^\( *\)\.id(\"ask_user\")/&\n\1.inputParams(java.util.Map.of(\"type\", \"object\", \"properties\", java.util.Map.of()))/" $g
  [ "$(grep -c "inputParams(java.util.Map.of(\"type\", \"object\", \"properties\", java.util.Map.of()))" $g)" = 1 ] || { echo "ask_user adaptation did not apply exactly once in $a"; exit 7; }
done
mvn -B -q $s -f /build-w06/pom.xml -pl agents -am -DskipTests install
mvn -B -q $s -f /build-runtime/pom.xml -DskipTests package
cp /build-runtime/target/w06-agent-runtime.jar /out/
sha256sum /out/w06-agent-runtime.jar /root/.m2/repository/com/agenthub/w06/agents/0.1.0-SNAPSHOT/agents-0.1.0-SNAPSHOT.jar
'
