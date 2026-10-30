package apt

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"serverupdater/internal/model"
)

type exitStatus int

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(int(e)) }
func (e exitStatus) ExitCode() int { return int(e) }

type fixtureRunner struct {
	commands            [][]string
	holds               string
	candidate           string
	simulation          string
	updateErr           error
	installedOutput     string
	policyOutput        string
	executionSimulation string
}

func fixture() *fixtureRunner {
	return &fixtureRunner{candidate: "2", simulation: "Inst foo [1] (2 Debian:13/stable [amd64])\nInst dependency (1 Debian:13/stable [all])\nConf foo (2 Debian:13/stable [amd64])\nConf dependency (1 Debian:13/stable [all])\n"}
}
func (f *fixtureRunner) Run(ctx context.Context, args []string, emit func(string)) ([]byte, error) {
	f.commands = append(f.commands, append([]string(nil), args...))
	switch args[0] {
	case "/usr/bin/dpkg":
		if args[1] == "--print-architecture" {
			return []byte("amd64\n"), nil
		}
		a, _ := strconv.Atoi(args[2])
		b, _ := strconv.Atoi(args[4])
		if a < b {
			return nil, nil
		}
		return nil, exitStatus(1)
	case "/usr/bin/dpkg-query":
		if f.installedOutput != "" {
			return []byte(f.installedOutput), nil
		}
		return []byte("foo\tamd64\t1\tinstalled\tno\tno\ncommon\tall\t1\tinstalled\tno\tno\nforeign\ti386\t1\tinstalled\tno\tno\nremoved\tamd64\t1\tconfig-files\tno\tno\n"), nil
	case "/usr/bin/apt-cache":
		if f.policyOutput != "" {
			return []byte(f.policyOutput), nil
		}
		return []byte(fmt.Sprintf("foo:\n  Installed: 1\n  Candidate: %s\ncommon:\n  Installed: 1\n  Candidate: 2\nforeign:i386:\n  Installed: 1\n  Candidate: 1\n", f.candidate)), nil
	case "/usr/bin/apt-mark":
		if args[1] == "showhold" {
			return []byte(f.holds), nil
		}
		if args[1] == "showauto" {
			return []byte("foo\ncommon\n"), nil
		}
		return nil, nil
	case "/usr/bin/apt-get":
		for _, a := range args {
			if a == "update" {
				return nil, f.updateErr
			}
		}
		for _, a := range args {
			if a == "--simulate" {
				if f.executionSimulation != "" && contains(args, "dependency:all=1") {
					return []byte(f.executionSimulation), nil
				}
				if emit != nil {
					emit(f.simulation)
				}
				return []byte(f.simulation), nil
			}
		}
		if emit != nil {
			emit("Installation abgeschlossen\n")
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unerwarteter Befehl: %v", args)
}

func TestInventoryAndScanArchitectures(t *testing.T) {
	f := fixture()
	f.holds = "common\n"
	m := Manager{Runner: f}
	i, err := m.inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if i.Packages["common:all"].Candidate != "2" || !i.Packages["common:all"].Held {
		t.Fatalf("all-Architektur: %+v", i.Packages)
	}
	u, err := m.scan(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if len(u) != 2 || u[0].ID != "common:all" || !u[0].Held || u[1].ID != "foo:amd64" {
		t.Fatalf("Updates: %+v", u)
	}
}

func TestPlanIncludesNewDependenciesAndPinsConfirmedVersions(t *testing.T) {
	f := fixture()
	m := Manager{Runner: f}
	p, err := m.Plan(context.Background(), []string{"foo:amd64"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 || p.Actions[0].Package != "dependency:all" || p.Actions[0].From != "" || p.Actions[1].From != "1" {
		t.Fatalf("Vorschau: %+v", p)
	}
	if err = m.Install(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	var install, marks []string
	for _, c := range f.commands {
		if c[0] == "/usr/bin/apt-get" && contains(c, "--assume-yes") {
			install = c
		}
		if c[0] == "/usr/bin/apt-mark" && c[1] == "auto" {
			marks = c
		}
	}
	for _, arg := range []string{"--no-remove", "--no-install-recommends", "dependency:all=1", "foo:amd64=2", "Dpkg::Options::=--force-confold"} {
		if !contains(install, arg) {
			t.Fatalf("%s fehlt: %v", arg, install)
		}
	}
	if !reflect.DeepEqual(marks, []string{"/usr/bin/apt-mark", "auto", "dependency:all", "foo:amd64"}) {
		t.Fatalf("APT-Automatik: %v", marks)
	}
}

type runnerFunc func(context.Context, []string, func(string)) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, args []string, emit func(string)) ([]byte, error) {
	return f(ctx, args, emit)
}

func TestReportedARMHFSimulationLine(t *testing.T) {
	const old = "1:9.16.50.1~deb11u5"
	const next = "1:9.16.50.1~deb11u6"
	const line = "Inst bind9-dnsutils [1:9.16.50.1~deb11u5] (1:9.16.50.1~deb11u6 Rasbian:oldstable [armhf])[]"
	const id = "bind9-dnsutils:armhf"
	// Native amd64 deliberately differs from the printed architecture: the trailing
	// annotation must not prevent extracting armhf or finding the installed version.
	i := inventory{Native: "amd64", Packages: map[string]installed{id: {ID: id, Version: old}}}
	compared := false
	m := Manager{Runner: runnerFunc(func(_ context.Context, args []string, _ func(string)) ([]byte, error) {
		want := []string{"/usr/bin/dpkg", "--compare-versions", old, "lt", next}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("Versionsvergleich: %v", args)
		}
		compared = true
		return nil, nil
	})}
	actions, err := m.parseActions(context.Background(), i, line)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Action{{Package: id, From: old, To: next}}
	if !compared || !reflect.DeepEqual(actions, want) {
		t.Fatalf("ARM-Vorschau: %+v", actions)
	}
}

func TestAPTDependencyAnnotationsInPreviewAndRevalidation(t *testing.T) {
	for _, suffix := range []string{"", "[]", " []", " [common:all ]", "\t[common:all foreign:i386 ]"} {
		t.Run("Suffix_"+suffix, func(t *testing.T) {
			f := fixture()
			f.simulation = "Inst foo [1] (2 Debian:13/stable [amd64])" + suffix + "\r\nInst dependency (1 Debian:13/stable [all])" + suffix + "\n"
			m := Manager{Runner: f}
			p, err := m.Plan(context.Background(), []string{"foo:amd64"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Actions) != 2 || p.Actions[0].Package != "dependency:all" || p.Actions[1].Package != "foo:amd64" || p.Actions[1].From != "1" {
				t.Fatalf("Vorschau: %+v", p.Actions)
			}
			// Status annotations describe individual simulation steps, not a change
			// to the resulting package versions that the user confirmed.
			f.simulation = fixture().simulation
			if err = m.Install(context.Background(), p, false, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func jellyfinFixture() *fixtureRunner {
	f := fixture()
	f.installedOutput = "jellyfin\tamd64\t1\tinstalled\tno\tno\njellyfin-ffmpeg7\tamd64\t7\tinstalled\tno\tno\n"
	f.policyOutput = "jellyfin:\n  Installed: 1\n  Candidate: 2\njellyfin-ffmpeg7:\n  Installed: 7\n  Candidate: 7\n"
	f.simulation = "Remv jellyfin-ffmpeg7 [7]\nInst jellyfin-ffmpeg8 (8 Jellyfin [amd64])\nInst jellyfin [1] (2 Jellyfin [amd64])\n"
	return f
}

func TestJellyfinReplacementRequiresExplicitConfirmation(t *testing.T) {
	f := jellyfinFixture()
	m := Manager{Runner: f}
	p, err := m.Plan(context.Background(), []string{"jellyfin:amd64"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Action{{Package: "jellyfin-ffmpeg7:amd64", From: "7", Remove: true}, {Package: "jellyfin-ffmpeg8:amd64", To: "8"}, {Package: "jellyfin:amd64", From: "1", To: "2"}}
	if !p.HasRemovals() || !reflect.DeepEqual(p.Actions, want) {
		t.Fatalf("unvollständige Ersatzvorschau: %+v", p.Actions)
	}
	if err = m.Install(context.Background(), p, false, nil); err == nil {
		t.Fatal("Entfernung ohne Bestätigung gestartet")
	}
	for _, cmd := range f.commands {
		if contains(cmd, "--assume-yes") {
			t.Fatal("Installation ohne Bestätigung ausgeführt")
		}
	}
	if err = m.Install(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	var installation []string
	for _, cmd := range f.commands {
		if contains(cmd, "--assume-yes") {
			installation = cmd
		}
	}
	for _, arg := range []string{"jellyfin:amd64=2", "jellyfin-ffmpeg7:amd64-", "jellyfin-ffmpeg8:amd64=8", "--no-allow-remove-essential", "--no-allow-downgrades", "--no-allow-change-held-packages", "APT::Get::Purge=false", "DPkg::Lock::Timeout=0"} {
		if !contains(installation, arg) {
			t.Fatalf("Bestätigter Plan fehlt in Installation: %s, %v", arg, installation)
		}
	}
	if contains(installation, "--no-remove") {
		t.Fatal("bestätigte Entfernung weiterhin gesperrt")
	}
	for _, cmd := range f.commands {
		if cmd[0] == "/usr/bin/apt-mark" && contains(cmd, "jellyfin-ffmpeg7:amd64") {
			t.Fatal("entferntes Paket für automatische Installation markiert")
		}
	}
}

func TestProtectedHeldAndChangedRemovalsAreRejected(t *testing.T) {
	for _, kind := range []string{"essential", "protected", "hold", "version", "unknown", "purge"} {
		t.Run(kind, func(t *testing.T) {
			f := jellyfinFixture()
			switch kind {
			case "essential":
				f.installedOutput = strings.Replace(f.installedOutput, "7\tinstalled\tno\tno", "7\tinstalled\tyes\tno", 1)
			case "protected":
				f.installedOutput = strings.Replace(f.installedOutput, "7\tinstalled\tno\tno", "7\tinstalled\tno\tyes", 1)
			case "hold":
				f.holds = "jellyfin-ffmpeg7\n"
			case "version":
				f.simulation = strings.Replace(f.simulation, "[7]", "[6]", 1)
			case "unknown":
				f.simulation = strings.Replace(f.simulation, "Remv jellyfin-ffmpeg7", "Remv missing", 1)
			case "purge":
				f.simulation = strings.Replace(f.simulation, "Remv ", "Purg ", 1)
			}
			m := Manager{Runner: f}
			if _, err := m.Plan(context.Background(), []string{"jellyfin:amd64"}, nil); err == nil {
				t.Fatalf("unerlaubte Entfernung zugelassen: %s", kind)
			}
		})
	}
	f := fixture()
	m := Manager{Runner: f}
	p, err := m.Plan(context.Background(), []string{"foo:amd64"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.executionSimulation = f.simulation + "Remv common [1]\n"
	if err = m.Install(context.Background(), p, true, nil); err == nil {
		t.Fatal("zusätzliche, nicht angezeigte Entfernung zugelassen")
	}
	for _, cmd := range f.commands {
		if contains(cmd, "--assume-yes") {
			t.Fatal("geänderten Ausführungsplan installiert")
		}
	}
}

func TestUnsafeSelectionsAndDependencyChanges(t *testing.T) {
	for _, tc := range []struct {
		name       string
		selected   []string
		holds, out string
	}{
		{"leer", nil, "", ""}, {"Option", []string{"--allow-unauthenticated"}, "", ""}, {"Paketmuster", []string{"foo.*:amd64"}, "", ""}, {"unbekannt", []string{"missing:amd64"}, "", ""}, {"doppelt", []string{"foo:amd64", "foo:amd64"}, "", ""},
		{"hold", []string{"foo:amd64"}, "foo\n", ""}, {"Purge", []string{"foo:amd64"}, "", "Purg common [1]\n"}, {"Downgrade", []string{"foo:amd64"}, "", "Inst foo [1] (0 Debian [amd64])\n"}, {"hold-Abhängigkeit", []string{"foo:amd64"}, "common\n", "Inst common [1] (2 Debian [all])\n"}, {"unbekannte-Ausgabe", []string{"foo:amd64"}, "", "Inst foo <unbekannt>\n"},
		{"Downgrade mit Statuszusatz", []string{"foo:amd64"}, "", "Inst foo [1] (0 Debian [amd64])[]\n"},
		{"hold-Abhängigkeit mit Statuszusatz", []string{"foo:amd64"}, "common\n", "Inst common [1] (2 Debian [all]) [foo:amd64 ]\n"},
		{"zurückgehaltene Entfernung", []string{"foo:amd64"}, "common\n", "Remv common [1] []\n"},
		{"unbekannter Zeilenzusatz", []string{"foo:amd64"}, "", "Inst foo [1] (2 Debian [amd64])[]unexpected\n"},
		{"unvollständiger Statuszusatz", []string{"foo:amd64"}, "", "Inst foo [1] (2 Debian [amd64])[common:all\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture()
			f.holds = tc.holds
			if tc.out != "" {
				f.simulation = tc.out
			}
			m := Manager{Runner: f}
			if _, err := m.Plan(context.Background(), tc.selected, nil); err == nil {
				t.Fatal("unsichere Auswahl angenommen")
			}
		})
	}
	for _, change := range []string{"Paketstand", "Abhängigkeiten", "Ablauf"} {
		t.Run(change, func(t *testing.T) {
			f := fixture()
			m := Manager{Runner: f}
			p, err := m.Plan(context.Background(), []string{"foo:amd64"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "Paketstand":
				f.candidate = "3"
			case "Abhängigkeiten":
				f.simulation += "Inst another (1 Debian [amd64])\n"
			case "Ablauf":
				p.ExpiresAt = time.Now().Add(-time.Second)
			}
			if err = m.Install(context.Background(), p, false, nil); err == nil {
				t.Fatal("geänderte Vorschau installiert")
			}
			for _, c := range f.commands {
				if contains(c, "--assume-yes") {
					t.Fatal("Installation trotz geänderter Vorschau gestartet")
				}
			}
		})
	}
}

func TestFailedRefreshIsNotSuccessful(t *testing.T) {
	f := fixture()
	f.updateErr = errors.New("Paketquelle nicht erreichbar")
	m := Manager{Runner: f}
	if _, err := m.Refresh(context.Background(), nil); err == nil {
		t.Fatal("Fehler verschluckt")
	}
}

func TestDebianVersionComparison(t *testing.T) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("dpkg nur auf Debian-Testsystemen")
	}
	m := Manager{Runner: ExecRunner{}}
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"1.0~rc1", "1.0", true}, {"1:1.0", "2.0", false}, {"1.0-2", "1.0-10", true}, {"1.0", "1.0-0", false}, {"1.0+deb12u1", "1.0+deb12u2", true}} {
		got, err := m.newer(context.Background(), tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatalf("%s < %s: %v, %v", tc.a, tc.b, got, err)
		}
	}
}
func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func TestReadOnlySystemInventory(t *testing.T) {
	if _, err := exec.LookPath("dpkg-query"); err != nil {
		t.Skip("Debian erforderlich")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := Manager{Runner: ExecRunner{}}
	i, err := m.inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(i.Packages) == 0 || i.Hash == "" {
		t.Fatal("leerer Paketstand")
	}
	for id, p := range i.Packages {
		if strings.HasSuffix(id, ":all") && p.Candidate == "" {
			t.Fatalf("Architektur all ohne Kandidat: %s", id)
		}
	}
}
