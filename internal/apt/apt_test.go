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
)

type exitStatus int

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(int(e)) }
func (e exitStatus) ExitCode() int { return int(e) }

type fixtureRunner struct {
	commands   [][]string
	holds      string
	candidate  string
	simulation string
	updateErr  error
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
		return []byte("foo\tamd64\t1\tinstalled\ncommon\tall\t1\tinstalled\nforeign\ti386\t1\tinstalled\nremoved\tamd64\t1\tconfig-files\n"), nil
	case "/usr/bin/apt-cache":
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
	if err = m.Install(context.Background(), p, nil); err != nil {
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

func TestUnsafeSelectionsAndDependencyChanges(t *testing.T) {
	for _, tc := range []struct {
		name       string
		selected   []string
		holds, out string
	}{
		{"leer", nil, "", ""}, {"Option", []string{"--allow-unauthenticated"}, "", ""}, {"Paketmuster", []string{"foo.*:amd64"}, "", ""}, {"unbekannt", []string{"missing:amd64"}, "", ""}, {"doppelt", []string{"foo:amd64", "foo:amd64"}, "", ""},
		{"hold", []string{"foo:amd64"}, "foo\n", ""}, {"Entfernung", []string{"foo:amd64"}, "", "Remv common [1]\n"}, {"Downgrade", []string{"foo:amd64"}, "", "Inst foo [1] (0 Debian [amd64])\n"}, {"hold-Abhängigkeit", []string{"foo:amd64"}, "common\n", "Inst common [1] (2 Debian [all])\n"}, {"unbekannte-Ausgabe", []string{"foo:amd64"}, "", "Inst foo <unbekannt>\n"},
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
			if err = m.Install(context.Background(), p, nil); err == nil {
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
