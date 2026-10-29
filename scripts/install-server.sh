#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "Bitte als root ausführen: sudo sh scripts/install-server.sh PFAD_ZUR_SERVERDATEI" >&2
    exit 1
fi
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
    echo "Aufruf: sudo sh scripts/install-server.sh bin/serverupdaterd-linux-amd64" >&2
    exit 1
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname -- "$script_dir")
if [ ! -f "$project_dir/deploy/serverupdater.service" ]; then
    echo "deploy/serverupdater.service fehlt. Das vollständige Serverarchiv entpacken." >&2
    exit 1
fi
for program in apt-get apt-cache apt-mark dpkg dpkg-query systemctl systemd-run; do
    if ! command -v "$program" >/dev/null 2>&1; then
        echo "Benötigtes Programm fehlt: $program" >&2
        exit 1
    fi
done
if systemctl is-active --quiet serverupdater.service; then
    echo "Dienst anhalten; laufende Paketoperationen dürfen zuerst fertig werden …"
    systemctl stop serverupdater.service
fi
install -d -m 700 /etc/serverupdater /var/lib/serverupdater
install -m 755 "$1" /usr/local/bin/serverupdaterd
if [ ! -f /etc/serverupdater/config.json ]; then
    /usr/local/bin/serverupdaterd init
else
    echo "Bestehende Konfiguration und Zugangsdaten werden weiterverwendet."
fi
install -m 644 "$project_dir/deploy/serverupdater.service" /etc/systemd/system/serverupdater.service
systemctl daemon-reload
systemctl enable --now serverupdater.service
echo "ServerUpdater läuft. HTTPS-Port 8443 in der Server-/Provider-Firewall freigeben."
echo "Protokoll: journalctl -u serverupdater -f"
