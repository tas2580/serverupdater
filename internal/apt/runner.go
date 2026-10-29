package apt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Runner interface {
	Run(context.Context, []string, func(string)) ([]byte, error)
}

type ExecRunner struct{}

type cappedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	const max = 32 << 20
	if len(p) > max-b.Len() {
		p = p[:max-b.Len()]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

type logWriter struct {
	mu   *sync.Mutex
	emit func(string)
}

func (w logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.emit != nil {
		w.emit(string(p))
	}
	return len(p), nil
}

func (ExecRunner) Run(ctx context.Context, args []string, emit func(string)) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("leerer Befehl")
	}
	allowed := map[string]bool{"/usr/bin/apt-get": true, "/usr/bin/apt-cache": true, "/usr/bin/apt-mark": true, "/usr/bin/dpkg": true, "/usr/bin/dpkg-query": true, "/usr/bin/systemd-run": true}
	if !allowed[args[0]] {
		return nil, errors.New("nicht zugelassener Befehl")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Do not inherit APT_CONFIG, LD_PRELOAD, proxy overrides, locale, or shell settings.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C", "LANG=C", "DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none", "NEEDRESTART_MODE=a", "UCF_FORCE_CONFFOLD=1"}
	cmd.Stdin = nil
	var out, stderr cappedBuffer
	lw := logWriter{mu: new(sync.Mutex), emit: emit}
	cmd.Stdout = io.MultiWriter(&out, lw)
	cmd.Stderr = io.MultiWriter(&stderr, lw)
	err := cmd.Run()
	if out.overflow || stderr.overflow {
		return nil, errors.New("Befehlsausgabe überschreitet 32 MiB")
	}
	if err != nil {
		if ctx.Err() != nil {
			return out.Bytes(), ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 4096 {
			detail = detail[len(detail)-4096:]
		}
		return out.Bytes(), fmt.Errorf("%s: %w: %s", args[0], err, detail)
	}
	return out.Bytes(), nil
}

func isExitOne(err error) bool {
	var ee interface{ ExitCode() int }
	return errors.As(err, &ee) && ee.ExitCode() == 1
}

// Available is checked at daemon startup, not when importing the GUI or running tests.
func Available() error {
	for _, p := range []string{"/usr/bin/apt-get", "/usr/bin/apt-cache", "/usr/bin/apt-mark", "/usr/bin/dpkg", "/usr/bin/dpkg-query", "/usr/bin/systemd-run"} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("benötigtes Programm %s: %w", p, err)
		}
	}
	return nil
}
