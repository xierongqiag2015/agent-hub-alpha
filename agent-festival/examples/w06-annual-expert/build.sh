#!/bin/sh
# Build the W06 annual expert in a throwaway Maven container.
# Usage: build.sh <W06 source .../source/w06-annual-report-agent> <W03/W04 experts-java dir at 4004ab4> <output dir>
# 1. W06 library: copy, adapt (../w06-agent-runtime/adapt-w06.sh), install;
# 2. platform layer: copy the pinned W03/W04 files unchanged into the build (platform.sha256 must match);
# 3. package. Proxy settings come from MAVEN_SETTINGS (optional).
set -e
here=$(cd "$(dirname "$0")" && pwd)
w06=$(cd "$1" && pwd)
experts=$(cd "$2" && pwd)
out=$(mkdir -p "$3" && cd "$3" && pwd)
settings=${MAVEN_SETTINGS:-}
docker run --rm -v "$w06":/w06:ro -v "$experts":/experts:ro -v "$here":/module:ro -v "$here/../w06-agent-runtime":/runtime:ro \
  -v "$out":/out ${settings:+-v "$settings":/settings.xml:ro} -v w05-m2:/root/.m2 maven:3.9-eclipse-temurin-21 sh -c '
set -e
s=""; [ -f /settings.xml ] && s="-s /settings.xml"
cp -r /w06 /build-w06 && sh /runtime/adapt-w06.sh /build-w06
mvn -B -q $s -f /build-w06/pom.xml -pl agents -am -DskipTests install
cp -r /module /build && cd /experts
while read sum file; do mkdir -p /build/$(dirname $file); cp $file /build/$file; done < /module/platform.sha256
cd /build && sha256sum -c platform.sha256
mvn -B -q $s -f /build/pom.xml -DskipTests package
cp /build/target/w06-annual-expert.jar /out/
sha256sum /out/w06-annual-expert.jar
'
