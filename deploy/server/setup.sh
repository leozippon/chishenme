#!/usr/bin/env bash
# Prepare the meal planner on a host that already runs the ledger. Safe to
# re-run: an existing /etc/chishenme/env, certificate and database are left
# alone. The binary and systemd unit are installed by deploy/deploy.sh.
set -euo pipefail

[[ $(id -u) -eq 0 ]] || { printf 'setup: must run as root\n' >&2; exit 1; }

PUBLIC_IP=${1:-}
log() { printf 'setup: %s\n' "$*"; }

random_string() {
	local value
	value=$(openssl rand -base64 96 | tr -dc 'A-Za-z0-9')
	printf '%s' "${value:0:$1}"
}

if ! id -u chishenme >/dev/null 2>&1; then
	useradd --system --home-dir /var/lib/chishenme --shell /usr/sbin/nologin chishenme
fi
install -d -o chishenme -g chishenme -m 0750 /var/lib/chishenme
install -d -o root -g chishenme -m 0750 /etc/chishenme

env_file=/etc/chishenme/env
umask 077
if [[ ! -e $env_file ]]; then
	admin=$(awk -F= '$1=="LEDGER_ADMIN_USER"{print substr($0, index($0, "=")+1)}' /etc/ledger/env 2>/dev/null || true)
	[[ -n $admin ]] || admin=zip
	password=$(random_string 16)
	secret=$(random_string 48)
	python3 - "$env_file" "$admin" "$password" "$secret" <<'PY'
import pathlib, sys
path, admin, password, secret = sys.argv[1:]
if any(ch in admin + password + secret for ch in "\n\r=# "):
    raise SystemExit("setup: generated account values must stay on one line")
key = ""
ledger = pathlib.Path("/etc/ledger/env")
if ledger.is_file():
    for line in ledger.read_text().splitlines():
        if line.startswith("LEDGER_DEEPSEEK_KEY="):
            key = line.split("=", 1)[1].strip()
            break
if key and not key.isascii():
    raise SystemExit("setup: deepseek key is not a single-line token")
text = "\n".join([
    "CHISHENME_ADDR=:18082",
    "CHISHENME_DB=/var/lib/chishenme/chishenme.db",
    f"CHISHENME_ADMIN_USER={admin}",
    f"CHISHENME_ADMIN_PASSWORD={password}",
    f"CHISHENME_SECRET={secret}",
    "CHISHENME_TLS_CERT=/etc/chishenme/tls.crt",
    "CHISHENME_TLS_KEY=/etc/chishenme/tls.key",
    f"CHISHENME_DEEPSEEK_KEY={key}",
    "",
])
pathlib.Path(path).write_text(text)
print("CHISHENME_RECIPE=" + ("yes" if key else "no"))
PY
	chown root:chishenme "$env_file"
	chmod 0640 "$env_file"
	printf 'CHISHENME_INITIAL_USER=%s\n' "$admin"
	printf 'CHISHENME_INITIAL_PASSWORD=%s\n' "$password"
else
	log "$env_file exists, left untouched"
	if awk -F= '$1=="CHISHENME_DEEPSEEK_KEY" && length($2)>0 {found=1} END{exit !found}' "$env_file"; then
		echo "CHISHENME_RECIPE=yes"
	else
		echo "CHISHENME_RECIPE=no"
	fi
fi

crt=/etc/chishenme/tls.crt
key=/etc/chishenme/tls.key
if [[ ! -e $crt && ! -e $key ]]; then
	if [[ -z $PUBLIC_IP ]]; then
		PUBLIC_IP=$(openssl x509 -in /etc/ledger/tls.crt -noout -ext subjectAltName 2>/dev/null | sed -n 's/.*IP Address:\([0-9.]*\).*/\1/p' | head -1)
	fi
	[[ $PUBLIC_IP =~ ^[0-9]+(\.[0-9]+){3}$ ]] || { printf 'setup: pass the public IP\n' >&2; exit 1; }
	log "writing a certificate for ${PUBLIC_IP}"
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
		-keyout "$key" -out "$crt" -subj "/CN=zip-private" \
		-addext "subjectAltName=IP:${PUBLIC_IP},DNS:zip-private" >/dev/null 2>&1
	chown root:chishenme "$key" "$crt"
	chmod 0640 "$key"
	chmod 0644 "$crt"
elif [[ -e $crt && -e $key ]]; then
	log "certificate exists, left untouched"
else
	printf 'setup: only one of the certificate files exists\n' >&2
	exit 1
fi

python3 - <<'PY'
from pathlib import Path
import re
path = Path("/etc/nftables.conf")
text = path.read_text()
match = re.search(r"tcp dport \{([^}]+)\} accept", text)
if not match:
    raise SystemExit("setup: nftables has no tcp dport rule")
ports = [part.strip() for part in match.group(1).split(",") if part.strip()]
if "18082" in ports:
    print("setup: port 18082 already open")
    raise SystemExit(0)
ports.append("18082")
ordered = sorted(set(ports), key=int)
rule = "tcp dport { " + ", ".join(ordered) + " } accept"
updated = text[:match.start()] + rule + text[match.end():]
Path("/etc/nftables.conf.new").write_text(updated)
print("setup: prepared port 18082")
PY
if [[ -f /etc/nftables.conf.new ]]; then
	nft -c -f /etc/nftables.conf.new
	mv -f /etc/nftables.conf.new /etc/nftables.conf
	nft -f /etc/nftables.conf
	log "opened port 18082"
fi
log "firewall loaded"
