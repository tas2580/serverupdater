//go:build linux

package apt

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// F_GETLK observes the POSIX locks used by APT/dpkg without taking or deleting them.
func checkPackageLocks(paths []string) error {
	for _, path := range paths {
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("APT-Sperre prüfen: %w", err)
		}
		lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
		err = unix.FcntlFlock(f.Fd(), unix.F_GETLK, &lock)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("APT-Sperre prüfen: %w", err)
		}
		if lock.Type != unix.F_UNLCK {
			return fmt.Errorf("Neustart gesperrt: externe Paketverwaltung hält %s (PID %d)", path, lock.Pid)
		}
	}
	return nil
}

func checkSystemPackageLocks() error {
	return checkPackageLocks([]string{"/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock", "/var/lib/apt/lists/lock", "/var/cache/apt/archives/lock"})
}
