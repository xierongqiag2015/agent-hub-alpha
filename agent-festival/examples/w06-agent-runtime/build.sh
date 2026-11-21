#!/bin/sh
# Build the W06 Bus runtime jar in a throwaway Maven container.
# Usage: build.sh <W06 source dir: .../W06-wanshoulu-1117/source/w06-annual-report-agent> <output dir>
# 1. copy the W06 source, apply the one-line adaptations (adapt-w06.sh; patches/w06-pr36-adaptations.patch),
#    install the W06 agents library into the container's Maven repository;
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
sh /build-runtime/adapt-w06.sh /build-w06
mvn -B -q $s -f /build-w06/pom.xml -pl agents -am -DskipTests install
mvn -B -q $s -f /build-runtime/pom.xml -DskipTests package
cp /build-runtime/target/w06-agent-runtime.jar /out/
sha256sum /out/w06-agent-runtime.jar /root/.m2/repository/com/agenthub/w06/agents/0.1.0-SNAPSHOT/agents-0.1.0-SNAPSHOT.jar
'
