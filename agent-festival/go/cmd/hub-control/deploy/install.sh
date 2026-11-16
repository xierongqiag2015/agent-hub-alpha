#!/bin/sh
# Install or upgrade hub-control on a systemd host. Run as root from the
# directory holding: hub-control (linux/amd64 binary), bootstrap.json,
# hub-control.service. Secrets are generated once and never printed.
set -eu
umask 077
install -d -m 0755 /opt/hub-control /opt/hub-control/bin
install -d -m 0755 /opt/hub-control/etc   # secrets.env inside stays 0600; systemd reads it as root
install -m 0755 hub-control /opt/hub-control/bin/hub-control.new
mv /opt/hub-control/bin/hub-control.new /opt/hub-control/bin/hub-control
install -m 0644 bootstrap.json /opt/hub-control/etc/bootstrap.json
# generate any missing account secret; existing ones are never replaced
touch /opt/hub-control/etc/secrets.env
chmod 0600 /opt/hub-control/etc/secrets.env
for v in DEMO_APPROVER DEMO_VIEWER DEMO_CURATOR HUB_ADMIN PLANNER GRAPH_HOST CHECKER_CALC CHECKER_CITATION; do
  grep -q "^HUB_SECRET_${v}=" /opt/hub-control/etc/secrets.env ||
    printf 'HUB_SECRET_%s=%s\n' "$v" "$(head -c 24 /dev/urandom | base64 | tr -d '/+=')" >> /opt/hub-control/etc/secrets.env
done
install -m 0644 hub-control.service /etc/systemd/system/hub-control.service
systemctl daemon-reload
systemctl enable hub-control >/dev/null 2>&1
systemctl restart hub-control
sleep 1
systemctl --no-pager --lines=0 status hub-control | head -5
