# ServerUpdater

Debian-Server überwachen und ausgewählte Paketupdates installieren: mit einem Go-Serverdienst und einem nativen Go-Desktop-Client für Windows und Linux. Ohne Docker, Browseroberfläche oder Datenbank.

Der Client verbindet sich mit der festen IP des Servers über HTTPS. Auf dem Client müssen keine Ports geöffnet werden. Mehrere Server lassen sich als getrennte Profile speichern.

## Funktionen

- CPU-Auslastung, Load Average, RAM, Laufzeit und verfügbarer Speicherplatz lokaler Dateisysteme; Messintervall fünf Sekunden.
- Automatische Paketprüfung beim Dienststart und standardmäßig jede Stunde, auch bei geschlossenem Client.
- Paketliste mit Suche, alten/neuen Versionen, Einzelauswahl und Kennzeichnung zurückgehaltener Pakete.
- Vor jeder Installation eine bestätigungspflichtige Vorschau einschließlich zusätzlicher Abhängigkeiten.
- Erforderliche Paketentfernungen, etwa `jellyfin-ffmpeg7` beim Wechsel auf `jellyfin-ffmpeg8`, werden separat angezeigt und benötigen eine zusätzliche Bestätigungs-Checkbox. Essenzielle, geschützte und zurückgehaltene Pakete können darüber nicht entfernt werden.
- Serverseitige Hintergrundaufträge mit Status und Protokoll; Installationen laufen bei geschlossener oder unterbrochener Clientverbindung weiter.
- **Separater Button „Server neu starten“: jederzeit unabhängig von Updates, mit Bestätigung des Servernamens.** Während eigener Paketoperationen ist der Neustart gesperrt; vor der Beauftragung werden außerdem externe APT-/dpkg-Sperren geprüft. Der Client verbindet sich anschließend erneut.
- Zertifikatsprüfung über einen fest hinterlegten SHA-256-Fingerabdruck und zufälliges Zugangstoken.

Zielplattformen: Debian 12/13 (amd64, arm64 und armhf/ARMv7) für den Server, Windows/Linux amd64 für den Client. Der Server wird statisch ohne CGo gebaut. Der Client verwendet Fyne und benötigt auf Linux eine grafische Sitzung mit OpenGL/X11 beziehungsweise XWayland.

## Schnellstart mit fertigen Dateien

Die erzeugten Programme liegen in `bin/`, Server- und Clientarchive unter `dist/`. Das Windows-Clientarchiv ist eine ZIP-Datei und lässt sich mit dem Explorer entpacken.
Lizenzhinweise zu mitgelieferten Bibliotheken stehen in `THIRD_PARTY_NOTICES.txt` und sollen bei einer Weitergabe der Programme beiliegen.

### 1. Server installieren

Das universelle Archiv `serverupdater-server-linux.tar.gz` enthält alle drei unterstützten Serverarchitekturen. Der Installer wählt die passende Datei automatisch. Alternativ mit `dpkg --print-architecture` die Serverarchitektur ermitteln und das kleinere passende Archiv auf den Debian-Server kopieren:

| Debian-Architektur | Serverarchiv |
| --- | --- |
| `amd64` | `serverupdater-server-linux-amd64.tar.gz` |
| `arm64` (64-Bit-ARM) | `serverupdater-server-linux-arm64.tar.gz` |
| `armhf` (32-Bit-ARM, mindestens ARMv7) | `serverupdater-server-linux-armhf.tar.gz` |

Mit dem universellen Archiv:

```sh
tar -xzf serverupdater-server-linux.tar.gz
sudo sh scripts/install-server.sh
```

Der Installer wählt die passende Datei aus `bin/` automatisch. Alternativ lässt sich ihr Pfad als Argument angeben. Die tatsächliche ELF-Architektur wird vor dem Anhalten oder Überschreiben eines Dienstes geprüft; eine falsche Datei wird auch dann abgelehnt, wenn sie umbenannt wurde. Mit `sh scripts/install-server.sh --check` lässt sich diese Prüfung ohne Installation und ohne Root-Rechte ausführen.

`Exec format error` bedeutet gewöhnlich, dass eine unpassende Prozessorarchitektur gewählt wurde. `arm64` und `armhf` benötigen unterschiedliche Dateien. Nach einem solchen Fehler das passende Archiv entpacken und den Installer erneut ausführen; eine vorhandene Serverkonfiguration wird weiterverwendet.

Das Installationsskript richtet den systemd-Dienst ein und gibt bei der ersten Installation **Zugangstoken und Zertifikatsfingerabdruck** aus. Diese Werte in den Client übernehmen. Der Server speichert ausschließlich einen Hash des Tokens; das ursprüngliche Token lässt sich später nicht auslesen.

Den TCP-Port **8443** in der Server-/Provider-Firewall erreichbar machen. Das Skript verändert keine Firewallregeln. Der Server benötigt außerdem Zugriff auf seine vorhandenen APT-Paketquellen.

### 2. Client starten und verbinden

- Windows: `serverupdater-windows-amd64.exe` doppelt anklicken.
- Linux: `chmod +x serverupdater-linux-amd64` und `./serverupdater-linux-amd64` in der grafischen Sitzung starten.

Unter „Server hinzufügen“ eintragen:

| Feld | Beispiel |
| --- | --- |
| Name | Mein Debian-Server |
| HTTPS-Adresse | `https://203.0.113.10:8443` |
| Zertifikatsfingerabdruck | SHA-256-Wert aus der Einrichtung |
| Zugangstoken | Token aus der Einrichtung |

IPv6 funktioniert mit eckigen Klammern, etwa `https://[2001:db8::10]:8443`. Eine Domain ist ebenfalls möglich. Den Fingerabdruck über eine bereits vertrauenswürdige Serverkonsole/SSH-Verbindung übernehmen. Ein abweichendes oder abgelaufenes Zertifikat wird abgewiesen.

Tokens werden unter Windows im Credential Manager und unter Linux über Secret Service, etwa GNOME Keyring, gespeichert. Fehlt der Schlüsselspeicher oder ist er gesperrt, bleibt das Token nur bis zum Schließen des Clients gespeichert. Die übrigen Profile liegen unter `%AppData%\serverupdater\profiles.json` beziehungsweise `~/.config/serverupdater/profiles.json` (`XDG_CONFIG_HOME` wird berücksichtigt). Über `--profiles /pfad/profiles.json` lässt sich der Pfad ändern; die Datei enthält keine Tokens.

### 3. Updates oder Neustart auslösen

Pakete markieren, „Updatevorschau“ anklicken und die vollständige Änderungsliste prüfen. Erst „Jetzt installieren“ startet die Installation. Neu benötigte Pakete werden angezeigt und mit installiert. Erforderliche Entfernungen stehen separat unter „WIRD ENTFERNT“ mit Paketname und installierter Version. Enthält die Vorschau Entfernungen, wird der Installationsknopf erst durch die zusätzliche Checkbox freigeschaltet. Auch der Server verlangt diese Zustimmung. Dafür Client und Server ab Version 0.2.0 verwenden.

Essenzielle, geschützte und zurückgehaltene Pakete werden nicht entfernt. Downgrades und vollständiges Löschen von Paketkonfigurationen (Purge) sind gesperrt. Ohne angezeigte und bestätigte Entfernungen bleibt auch die tatsächliche APT-Installation gegen Entfernungen gesperrt. Eine Vorschau gilt zehn Minuten; der Paketstand und der vollständige Ausführungsplan werden vor der Installation erneut geprüft. Bei Änderungen ist eine neue Bestätigung erforderlich.

Lokal geänderte Paketkonfigurationen werden behalten. Paket-Skripte und `needrestart` dürfen im Rahmen der Installation betroffene Dienste neu starten. Ein Serverneustart erfolgt ausschließlich über den gesonderten, bestätigungspflichtigen Neustartknopf. Ein fehlender `/run/reboot-required`-Hinweis garantiert auf Debian nicht, dass kein Neustart erforderlich ist.

Die Auftragsauswahl zeigt auch frühere Ergebnisse. Nach einer Wiederverbindung laufende oder abgeschlossene Aufträge dort prüfen, bevor ein Start wiederholt wird. Eine abgeschlossene Vorschau lässt sich über „Updatevorschau öffnen“ erneut ansehen.

## Serverkonfiguration und Betrieb

Standardpfade:

| Pfad | Inhalt |
| --- | --- |
| `/etc/serverupdater/config.json` | Listen-Adresse, Prüfintervall, Daten-/TLS-Pfade und Token-Hash |
| `/etc/serverupdater/server.crt` | Selbstsigniertes Serverzertifikat, zehn Jahre gültig |
| `/etc/serverupdater/server.key` | Privater TLS-Schlüssel |
| `/var/lib/serverupdater/scan.json` | Letzte erfolgreiche Paketliste und letzter Prüffehler |
| `/var/lib/serverupdater/jobs.json` | Letzte 100 Aufträge, je maximal etwa 256 KiB Ausgabe |

Die Einrichtung legt Konfiguration und private Daten mit eingeschränkten Dateirechten an. Root-Rechte sind für Paketverwaltung und Neustarts erforderlich. Das API bietet ausschließlich die beschriebenen Aktionen und keine allgemeine Befehlsausführung. Das Token berechtigt zu Updates und Neustarts; es gehört nur auf Rechner, die den Server administrieren dürfen.

Beispiel für eine alternative Einrichtung vor der Dienstinstallation:

```sh
sudo /usr/local/bin/serverupdaterd init --listen :9443 --check-interval 7200
```

`init` überschreibt keine bestehende Konfiguration oder TLS-Dateien. Eine bestehende Konfiguration kann direkt als root bearbeitet werden; `check_interval_seconds` muss mindestens 60 sein. Danach den Dienst neu starten. Für eine geänderte Adresse/Portnummer auch Clientprofile und Firewall anpassen.

```sh
sudo systemctl status serverupdater
sudo journalctl -u serverupdater -f
sudo systemctl restart serverupdater
sudo /usr/local/bin/serverupdaterd fingerprint
```

Ein Dienststopp wartet auf aktive Paketoperationen. Dadurch kann `systemctl stop/restart` während einer Installation länger dauern; APT/dpkg werden dabei nicht absichtlich beendet. Nach einem Absturz werden zuvor laufende Aufträge als unterbrochen markiert und nicht wiederholt. Eventuelle dpkg-Reparaturen über den normalen Administrationszugang durchführen.

Token wechseln, etwa bei verlorenem Client:

```sh
sudo /usr/local/bin/serverupdaterd rotate-token
sudo systemctl restart serverupdater
```

Das neue Token in allen Clientprofilen eintragen. Es gilt nach dem Dienstneustart. Für ein neues Zertifikat den Dienst stoppen, die bisherige Konfiguration/TLS-Identität sicher archivieren und `init` erneut ausführen; danach Token und Fingerabdruck in allen Clients neu hinterlegen. Bestehende Daten können weiterverwendet werden.

APT-Paketquellen, Pinning und Holds werden respektiert. Fehlgeschlagene Paketprüfungen lassen die letzte erfolgreiche Liste stehen und kennzeichnen sie als veraltet. Parallele eigene Operationen sind gesperrt; bei externen APT-Sperren wird normalerweise bis zu 60 Sekunden gewartet. Installationen mit bestätigten Entfernungen warten nicht durch eine fremde Paketinstallation: Nach deren Abschluss eine neue Vorschau erstellen. Die Paketquellen müssen zur gewünschten Debian-Version passen: Das Programm verwaltet keine Distributionswechsel, Paketquellen oder Rollbacks. Andere Paketmanager und automatische Paketbereinigung gehören nicht zum Funktionsumfang.

## Selbst bauen

Benötigt wird **Go 1.24 oder neuer**. Modulversionen und Prüfsummen stehen fest in `go.mod`/`go.sum`.

Nur den Server bauen, ohne GUI-Abhängigkeiten:

```sh
make server-all
```

Das baut Serverdateien für amd64, arm64 und armhf. `make server` verwendet die Architektur der lokalen Go-Installation. Nur 32-Bit-ARM: `make server-armhf` (`GOARCH=arm`, `GOARM=7`).

Alternativ ohne Make:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o serverupdaterd ./cmd/serverupdaterd
```

### Linux-Client

Auf einem Debian-Entwicklungsrechner mit installiertem Go:

```sh
sudo apt-get install gcc pkg-config libgl1-mesa-dev xorg-dev
make client-linux
```

Der Client wird gegen die Grafik- und C-Bibliotheken des Buildsystems gelinkt. Für ältere Linux-Clients auf einer entsprechend alten Distribution bauen, etwa Debian 12 für Debian-12-Clients. Der Server hat diese Einschränkung durch seinen statischen Build nicht.

### Windows-Client

Auf Windows Go und einen MinGW-w64-C-Compiler installieren und dessen `bin`-Verzeichnis in den PATH aufnehmen. In PowerShell:

```powershell
$env:CGO_ENABLED="1"
$env:CGO_LDFLAGS="-static"
go build -buildvcs=false -trimpath -ldflags="-H windowsgui" -o serverupdater.exe ./cmd/serverupdater
```

Cross-Build von Linux mit MinGW-w64:

```sh
sudo apt-get install gcc-mingw-w64-x86-64
make client-windows
```

Der Client enthält ein eigenes Server-Symbol für das Programmfenster und die Windows-EXE. Die SVG-Vorlage liegt unter `internal/branding/server.svg`; PNG, ICO und die Windows-Ressource sind bereits enthalten. Nach Änderungen an der Vorlage erzeugt `make icons windows-resource` diese Dateien neu (für die Windows-Ressource wird MinGW-w64 `windres` benötigt). Die ICO-Datei enthält Größen von 16 bis 256 Pixeln.

Alle fünf Builds und Archive: `make release` (Linux-Grafikheader, MinGW, GNU Make, tar und zip erforderlich). Bereits gebaute Dateien mit `make package` verpacken. Details zu den GUI-Buildvoraussetzungen: [Fyne Quick Start](https://docs.fyne.io/started/quick/).

## Tests

```sh
go test -tags ci ./...
go test -race -tags ci ./...
go vet -tags ci ./...
```

`ci` verwendet den Fyne-Treiber ohne Bildschirm. Paketinstallationen und Neustarts werden in den Tests durch Ersatzimplementierungen ausgeführt. Ein zusätzlicher Debian-Test liest den vorhandenen Paketstand und prüft echte Debian-Versionsvergleiche, ohne Paketquellen zu aktualisieren oder Pakete zu installieren.

Für die Abnahme auf einer separaten Debian-12/13-VM: Dienst einrichten, Profile auf Windows/Linux anlegen, Systemwerte vergleichen, Updates mit neuen Abhängigkeiten bestätigen, Client während einer Installation schließen, Ergebnisse nach Wiederverbindung prüfen und einen Neustart ohne Updates testen. Den Neustart außerdem während einer Paketoperation versuchen und die serverseitige Sperre prüfen. Ein tatsächlicher Windows-Start und reale Paketinstallationen/Neustarts müssen auf diesen Zielsystemen abgenommen werden.

## API für Erweiterungen

Alle Endpunkte unter `/api/v1` benötigen `Authorization: Bearer TOKEN`. Verändernde Aufrufe benötigen außerdem einen eindeutigen `Idempotency-Key` (8–128 Zeichen). Derselbe Schlüssel mit demselben Inhalt liefert innerhalb des gespeicherten Auftragsverlaufs denselben Auftrag zurück. Aufträge starten mit HTTP 202 und laufen unabhängig von der HTTP-Verbindung. Bei einer laufenden Operation wird HTTP 409 geliefert. JSON-Fehlerantworten enthalten `error`.

| Methode | Pfad | Inhalt/Ergebnis |
| --- | --- | --- |
| GET | `/status` | Hostname, Boot-ID, Version, aktive Operation, Neustarthinweis |
| GET | `/metrics` | Systemmesswerte mit Zeitstempel und Messfehlern |
| GET | `/updates` | Letzte Paketprüfung mit Paketliste und Fehlerstatus |
| GET | `/jobs` | Zusammenfassungen der letzten Aufträge, neueste zuerst |
| GET | `/jobs/{id}` | Auftrag mit Protokoll, Ergebnis und gegebenenfalls Vorschau |
| POST | `/check` | `{}`; Paketprüfung starten |
| POST | `/plans` | `{"packages":["openssl:amd64"]}`; Vorschauauftrag starten |
| POST | `/install` | `{"plan_id":"…","confirm_removals":true}`; `confirm_removals` ist bei geplanten Entfernungen ausdrücklich erforderlich, sonst optional/false |
| POST | `/reboot` | `{"confirm_hostname":"debian-server"}`; Neustart ohne Updatevoraussetzung |

Auftragszustände: `running`, `succeeded`, `failed`, `interrupted`. Der Neustartauftrag meldet die erfolgreiche Beauftragung von systemd; erst eine neue Boot-ID bestätigt den tatsächlichen Systemstart. Der Server blockiert nach der Neustartbeauftragung weitere Paketoperationen bis zum nächsten Dienststart. Anfragen mit Browser-Origin und HTTP-Weiterleitungen werden nicht unterstützt.

Vorschauaktionen enthalten bei Entfernungen `remove: true`, die installierte Version in `from` und ein leeres `to`. Ältere Clients können damit keine Entfernungen auslösen, da ihnen die zusätzliche Zustimmung im Installationsaufruf fehlt.
