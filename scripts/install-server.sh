#!/bin/sh
set -eu

usage() {
    echo "Installation: sudo sh scripts/install-server.sh [PFAD_ZUR_SERVERDATEI]"
    echo "Nur Architektur prüfen: sh scripts/install-server.sh --check [PFAD_ZUR_SERVERDATEI]"
    echo "Ohne Dateiangabe wird die passende Datei aus bin/ automatisch gewählt."
}
check_only=false
case "${1:-}" in
    --check) check_only=true; shift ;;
    --help|-h) usage; exit 0 ;;
    --*) usage >&2; exit 1 ;;
esac
if [ "$#" -gt 1 ]; then
    usage >&2
    exit 1
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname -- "$script_dir")
for program in dpkg od; do
    if ! command -v "$program" >/dev/null 2>&1; then
        echo "Benötigtes Programm fehlt: $program" >&2
        exit 1
    fi
done
server_arch=$(dpkg --print-architecture)
case "$server_arch" in
    amd64) expected_class=2; expected_machine=62; expected_header=64 ;;
    arm64) expected_class=2; expected_machine=183; expected_header=64 ;;
    armhf) expected_class=1; expected_machine=40; expected_header=52 ;;
    *)
        echo "Für Debian-Architektur $server_arch ist keine fertige Serverdatei vorhanden. Unterstützt: amd64, arm64, armhf (ARMv7)." >&2
        exit 1
        ;;
esac
binary_path=${1:-"$project_dir/bin/serverupdaterd-linux-$server_arch"}
if [ ! -f "$binary_path" ]; then
    echo "Serverarchitektur: $server_arch. Passende Datei fehlt: $binary_path" >&2
    echo "Bitte serverupdater-server-linux-$server_arch.tar.gz herunterladen und vollständig entpacken." >&2
    exit 1
fi
# Read the actual ELF header, rather than trusting a possibly renamed filename.
# od emits only decimal bytes; word splitting here is intentional.
header=$(LC_ALL=C od -An -v -tu1 -N64 -- "$binary_path")
set -- $header
if [ "$#" -lt 20 ] || [ "$1 $2 $3 $4" != "127 69 76 70" ]; then
    echo "Keine gültige Linux-ELF-Datei: $binary_path. Bitte das Serverarchiv für $server_arch verwenden." >&2
    exit 1
fi
case "$5:${19}:${20}" in
    2:62:0) binary_arch=amd64 ;;
    2:183:0) binary_arch=arm64 ;;
    1:40:0) binary_arch=armhf ;;
    *) binary_arch="unbekannt (ELF-Klasse $5, Maschinentyp ${19}/${20})" ;;
esac
if [ "$5" != "$expected_class" ] || [ "${19}" != "$expected_machine" ] || [ "${20}" != 0 ]; then
    echo "Falsche Serverdatei: Debian-Server ist $server_arch, die Datei ist $binary_arch." >&2
    echo "Bitte serverupdater-server-linux-$server_arch.tar.gz verwenden. Es wurde nichts am installierten Dienst geändert." >&2
    exit 1
fi
if [ "$#" -lt "$expected_header" ] || [ "$6" != 1 ] || [ "$7" != 1 ] || [ "${21:-0}" != 1 ]; then
    echo "Ungültiger oder unvollständiger ELF-Header: $binary_path" >&2
    exit 1
fi
case "${17}:${18}" in
    2:0|3:0) ;;
    *) echo "Datei ist kein ausführbares Linux-Programm: $binary_path" >&2; exit 1 ;;
esac
echo "Architektur bestätigt: Debian $server_arch, Datei $binary_path"
if [ "$check_only" = true ]; then
    exit 0
fi
if [ "$(id -u)" -ne 0 ]; then
    echo "Bitte als root ausführen: sudo sh scripts/install-server.sh [PFAD_ZUR_SERVERDATEI]" >&2
    exit 1
fi
if [ ! -f "$project_dir/deploy/serverupdater.service" ]; then
    echo "deploy/serverupdater.service fehlt. Das vollständige Serverarchiv entpacken." >&2
    exit 1
fi
for program in apt-get apt-cache apt-mark dpkg-query systemctl systemd-run install mktemp cp chmod rm; do
    if ! command -v "$program" >/dev/null 2>&1; then
        echo "Benötigtes Programm fehlt: $program" >&2
        exit 1
    fi
done
# Probe on the destination filesystem: /tmp or the download directory may be noexec.
# No existing service or executable is changed until the version probe succeeds.
if [ ! -d /usr/local/bin ]; then
    install -d -m 755 /usr/local/bin
fi
staging_dir=$(mktemp -d /usr/local/bin/.serverupdater-XXXXXX)
trap 'rm -rf -- "$staging_dir"' 0
trap 'exit 1' HUP INT TERM
cp -- "$binary_path" "$staging_dir/serverupdaterd"
chmod 755 "$staging_dir/serverupdaterd"
if ! "$staging_dir/serverupdaterd" version >/dev/null; then
    echo "Die Serverdatei lässt sich auf diesem System nicht ausführen. Bitte das Archiv für $server_arch erneut herunterladen. Der installierte Dienst wurde nicht geändert." >&2
    exit 1
fi
if systemctl is-active --quiet serverupdater.service; then
    echo "Dienst anhalten; laufende Paketoperationen dürfen zuerst fertig werden …"
    systemctl stop serverupdater.service
fi
install -d -m 700 /etc/serverupdater /var/lib/serverupdater
install -m 755 "$staging_dir/serverupdaterd" /usr/local/bin/serverupdaterd
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
