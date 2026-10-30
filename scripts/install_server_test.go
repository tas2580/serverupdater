package scripts

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type installFixture struct {
	root     string
	script   string
	bin      string
	commands string
	marker   string
}

func installer(t *testing.T, arch string) installFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Debian-Installer wird unter Linux geprüft")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX-Shell erforderlich")
	}
	root := t.TempDir()
	f := installFixture{root: root, script: filepath.Join(root, "scripts", "install-server.sh"), bin: filepath.Join(root, "bin"), commands: filepath.Join(root, "commands"), marker: filepath.Join(root, "unexpected-mutation")}
	for _, dir := range []string{filepath.Dir(f.script), f.bin, f.commands, filepath.Join(root, "deploy")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile("install-server.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.script, source, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "deploy", "serverupdater.service"), []byte("[Service]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mktemp, err := exec.LookPath("mktemp")
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{
		"dpkg":      "#!/bin/sh\nprintf '%s\\n' '" + arch + "'\n",
		"id":        "#!/bin/sh\nprintf '0\\n'\n",
		"systemctl": "#!/bin/sh\nprintf 'systemctl\\n' >> \"$SERVERUPDATER_TEST_MUTATION\"\nexit 99\n",
		"install":   "#!/bin/sh\nprintf 'install\\n' >> \"$SERVERUPDATER_TEST_MUTATION\"\nexit 99\n",
		"mktemp":    "#!/bin/sh\nexec '" + strings.ReplaceAll(mktemp, "'", "'\\''") + "' -d \"$SERVERUPDATER_TEST_STAGING\"/stage.XXXXXX\n",
	}
	for name, body := range commands {
		if err = os.WriteFile(filepath.Join(f.commands, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f installFixture) writeELF(t *testing.T, name, arch string) string {
	t.Helper()
	data := make([]byte, 64)
	copy(data, []byte{127, 'E', 'L', 'F'})
	data[4] = 2
	data[5] = 1
	data[6] = 1
	binary.LittleEndian.PutUint16(data[16:18], 2)
	binary.LittleEndian.PutUint32(data[20:24], 1)
	switch arch {
	case "amd64":
		binary.LittleEndian.PutUint16(data[18:20], 62)
	case "arm64":
		binary.LittleEndian.PutUint16(data[18:20], 183)
	case "armhf":
		data[4] = 1
		binary.LittleEndian.PutUint16(data[18:20], 40)
	default:
		t.Fatal("unbekannte Testarchitektur")
	}
	if data[4] == 1 {
		binary.LittleEndian.PutUint16(data[40:42], 52)
	} else {
		binary.LittleEndian.PutUint16(data[52:54], 64)
	}
	path := filepath.Join(f.bin, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f installFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{f.script}, args...)...)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "PATH="+f.commands+string(os.PathListSeparator)+os.Getenv("PATH"), "SERVERUPDATER_TEST_MUTATION="+f.marker, "SERVERUPDATER_TEST_STAGING="+f.root)
	output, err := cmd.CombinedOutput()
	if _, statErr := os.Stat(f.marker); !os.IsNotExist(statErr) {
		t.Fatalf("Dienst oder Installation vor Abschluss der Prüfungen verändert: %s", output)
	}
	return string(output), err
}

func TestInstallerDetectsSupportedArchitectureWithoutExecutingFile(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "armhf"} {
		t.Run(arch, func(t *testing.T) {
			f := installer(t, arch)
			f.writeELF(t, "serverupdaterd-linux-"+arch, arch)
			output, err := f.run(t, "--check")
			if err != nil || !strings.Contains(output, "Debian "+arch) {
				t.Fatalf("Architektur nicht erkannt: %v\n%s", err, output)
			}
		})
	}
}

func TestInstallerRejectsRenamedWrongArchitectureBeforeChangingService(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			f := installer(t, "armhf")
			path := f.writeELF(t, "serverupdaterd-linux-armhf", arch)
			output, err := f.run(t, path)
			if err == nil || !strings.Contains(output, "Falsche Serverdatei") || !strings.Contains(output, "Server ist armhf") {
				t.Fatalf("Falsche Datei nicht abgewiesen: %v\n%s", err, output)
			}
		})
	}
}

func TestInstallerRejectsMissingUnsupportedOrDamagedFiles(t *testing.T) {
	t.Run("fehlende armhf-Datei", func(t *testing.T) {
		f := installer(t, "armhf")
		f.writeELF(t, "serverupdaterd-linux-amd64", "amd64")
		output, err := f.run(t, "--check")
		if err == nil || !strings.Contains(output, "serverupdater-server-linux-armhf.tar.gz") {
			t.Fatalf("Kein passender Downloadhinweis: %v\n%s", err, output)
		}
	})
	t.Run("nicht unterstützte Architektur", func(t *testing.T) {
		f := installer(t, "riscv64")
		output, err := f.run(t, "--check")
		if err == nil || !strings.Contains(output, "keine fertige Serverdatei") {
			t.Fatalf("Architektur nicht abgewiesen: %v\n%s", err, output)
		}
	})
	for _, tc := range []struct {
		name string
		data []byte
	}{{"Windows-EXE", []byte("MZ Windows")}, {"leere Datei", nil}, {"abgeschnittener ELF-Header", []byte{127, 'E', 'L', 'F'}}} {
		t.Run(tc.name, func(t *testing.T) {
			f := installer(t, "armhf")
			path := filepath.Join(f.bin, "serverupdaterd-linux-armhf")
			if err := os.WriteFile(path, tc.data, 0644); err != nil {
				t.Fatal(err)
			}
			output, err := f.run(t, path)
			if err == nil || !strings.Contains(output, "Keine gültige Linux-ELF-Datei") {
				t.Fatalf("Ungültige Datei angenommen: %v\n%s", err, output)
			}
		})
	}
}

func TestInstallerProbesExecutionBeforeStoppingExistingService(t *testing.T) {
	arch := "amd64"
	switch runtime.GOARCH {
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "armhf"
	case "amd64":
	default:
		t.Skip("unbekannte Testarchitektur")
	}
	f := installer(t, arch)
	// This has a matching ELF header but no executable segments. The version probe must fail.
	path := f.writeELF(t, "serverupdaterd-linux-"+arch, arch)
	output, err := f.run(t, path)
	if err == nil || !strings.Contains(output, "lässt sich auf diesem System nicht ausführen") {
		t.Fatalf("Ausführung nicht vorab geprüft: %v\n%s", err, output)
	}
}
