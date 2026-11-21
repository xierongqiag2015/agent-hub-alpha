#!/bin/sh
# Apply the three one-line W05 adaptations to a copy of the W06 PR #36 source
# (patches/w06-pr36-adaptations.patch explains each). Stops if upstream already
# fixed one, if the text changed, or if a replacement does not hit exactly once.
# Usage: adapt-w06.sh <copy of .../source/w06-annual-report-agent>
set -e
a=$1/agents/src/main/java/com/agenthub/w06/agents
f=$a/material/MaterialAgentFactory.java
[ "$(grep -c "store.load();" $f)" = 0 ] || { echo "W06 source already loads the snapshot; drop the adaptation"; exit 2; }
sed -i "s/^\( *\)SnapshotStore store = new SnapshotStore(snapshotDir);/&\n\1store.load();/" $f
[ "$(grep -c "store.load();" $f)" = 1 ] || { echo "adaptation did not apply exactly once"; exit 3; }
m=$a/metrics/MetricAgentFactory.java
[ "$(grep -c "亿元/% 等" $m)" = 1 ] || { echo "metrics prompt changed upstream; review the adaptation"; exit 4; }
sed -i "s|亿元/% 等|亿元/%% 等|" $m
[ "$(grep -c "亿元/%% 等" $m)" = 1 ] || { echo "metrics adaptation did not apply exactly once"; exit 5; }
for r in metrics/MetricAgentFactory operation/OperationAgentFactory assetquality/AssetQualityAgentFactory writer/WriterAgentFactory; do
  g=$a/$r.java
  [ "$(grep -c "\.id(\"ask_user\")" $g)" = 1 ] || { echo "ask_user card changed upstream in $r; review the adaptation"; exit 6; }
  sed -i "s/^\( *\)\.id(\"ask_user\")/&\n\1.inputParams(java.util.Map.of(\"type\", \"object\", \"properties\", java.util.Map.of()))/" $g
  [ "$(grep -c "inputParams(java.util.Map.of(\"type\", \"object\", \"properties\", java.util.Map.of()))" $g)" = 1 ] || { echo "ask_user adaptation did not apply exactly once in $r"; exit 7; }
done
echo "W06 adaptations applied"
