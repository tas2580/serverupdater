//go:build linux

package apt

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestExternalAPTLockHelper(t *testing.T) {
	path := os.Getenv("SERVERUPDATER_TEST_APT_LOCK")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		os.Exit(2)
	}
	defer f.Close()
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock); err != nil {
		os.Exit(3)
	}
	fmt.Println("locked")
	_, _ = os.Stdin.Read(make([]byte, 1))
	os.Exit(0)
}

func TestExternalPackageLockBlocksReboot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dpkg.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExternalAPTLockHelper$")
	cmd.Env = append(os.Environ(), "SERVERUPDATER_TEST_APT_LOCK="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("Sperre nicht eingerichtet: %s %v", line, err)
	}
	if err = checkPackageLocks([]string{path}); err == nil {
		t.Fatal("externe Paketoperation nicht erkannt")
	}
	input.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = checkPackageLocks([]string{path}); err != nil {
		t.Fatalf("freigegebene Sperre weiterhin belegt: %v", err)
	}
}
