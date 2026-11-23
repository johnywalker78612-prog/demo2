#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TARGET=${HOSTINGER_TARGET:-root@179.198.202.47}
KEY=${HOSTINGER_SSH_KEY:-$HOME/.ssh/visa_uk_hostinger}
BUILD_DIR=$(mktemp -d)
trap 'rm -r "$BUILD_DIR"' EXIT
SSH=(ssh -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=15)
(cd "$ROOT/backend" && go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$BUILD_DIR/server" ./cmd/server)
REMOTE_DIR=$("${SSH[@]}" "$TARGET" 'mktemp -d /root/private-records-deploy.XXXXXXXX')
scp -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes "$BUILD_DIR/server" "$TARGET:$REMOTE_DIR/server"
"${SSH[@]}" "$TARGET" sh -s -- "$REMOTE_DIR" <<'REMOTE'
set -eu
stage=$1
exec 9>/run/private-records-deploy.lock
flock -n 9
cp -p /opt/private-records/server /opt/private-records/server.previous
install -m 755 "$stage/server" /opt/private-records/server.new
mv /opt/private-records/server.new /opt/private-records/server
healthy=false
if systemctl restart private-records; then
for attempt in 1 2 3 4 5 6 7 8 9 10; do
 if curl -fsS --max-time 5 http://localhost:10000/healthz >/dev/null && curl -fsS --max-time 10 http://localhost:10000/ >/dev/null; then
  healthy=true
  break
 fi
 sleep 2
done
fi
if [ "$healthy" != true ]; then
 cp -p /opt/private-records/server.previous /opt/private-records/server.rollback
 mv /opt/private-records/server.rollback /opt/private-records/server
 systemctl restart private-records
 echo 'Deployment failed health checks; restored previous executable.' >&2
 exit 1
fi
rm "$stage/server"
rmdir "$stage"
sha256sum /opt/private-records/server
systemctl is-active private-records
echo 'Deployment succeeded. Existing environment and database preserved.'
REMOTE
