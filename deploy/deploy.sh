#!/usr/bin/env bash
# Build the meal planner for Linux and install it on zip-private.
#
#   deploy/deploy.sh [ssh-host]
set -euo pipefail

HOST=${1:-zip-private}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

log() { printf '\n==> %s\n' "$*"; }
die() { printf 'deploy: %s\n' "$*" >&2; exit 1; }

log "Building Linux binary"
make build-linux

log "Preparing $HOST"
scp -q deploy/server/setup.sh "$HOST:/tmp/chishenme-setup.sh"
scp -q deploy/chishenme.service "$HOST:/etc/systemd/system/chishenme.service"
scp -q deploy/fail2ban/filter.d/chishenme.conf "$HOST:/etc/fail2ban/filter.d/chishenme.conf"
scp -q deploy/fail2ban/jail.d/chishenme.local "$HOST:/etc/fail2ban/jail.d/chishenme.local"
ssh "$HOST" 'bash /tmp/chishenme-setup.sh && rm -f /tmp/chishenme-setup.sh'

log "Installing binary"
scp -q dist/chishenme-linux-amd64 "$HOST:/usr/local/bin/chishenme.new"
ssh "$HOST" 'bash -s' <<'EOF'
set -euo pipefail
chmod 0755 /usr/local/bin/chishenme.new
mv -f /usr/local/bin/chishenme.new /usr/local/bin/chishenme
systemctl daemon-reload
systemctl enable chishenme
systemctl restart chishenme
systemctl reload fail2ban
systemctl is-active chishenme
EOF

log "Checking the login page"
if ! ssh "$HOST" 'curl -fsS --max-time 10 -k https://127.0.0.1:18082/ | grep -q "吃什么"'; then
	ssh "$HOST" 'journalctl -u chishenme -n 30 --no-pager' || true
	die "chishenme did not serve its page"
fi
ssh "$HOST" 'journalctl -u chishenme -n 8 --no-pager'
