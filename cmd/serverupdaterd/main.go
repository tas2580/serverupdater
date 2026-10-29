//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"serverupdater/internal/apt"
	"serverupdater/internal/files"
	"serverupdater/internal/identity"
	"serverupdater/internal/server"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("ServerUpdater", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "run"
	args := os.Args[1:]
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command = args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet("serverupdaterd "+command, flag.ContinueOnError)
	configPath := f.String("config", "/etc/serverupdater/config.json", "Pfad zur Konfiguration")
	dataDir := f.String("data-dir", "/var/lib/serverupdater", "Datenverzeichnis (init)")
	listen := f.String("listen", ":8443", "HTTPS-Listen-Adresse (init)")
	interval := f.Int("check-interval", 3600, "Sekunden zwischen Paketprüfungen (init)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unerwartete Argumente")
	}
	if command == "version" {
		fmt.Println(version)
		return nil
	}
	if command == "init" {
		c, token, err := identity.Init(*configPath, *dataDir, *listen, *interval)
		if err != nil {
			return err
		}
		fp, err := identity.Fingerprint(c.Certificate)
		if err != nil {
			return err
		}
		fmt.Printf("Konfiguration: %s\nHTTPS-Port: %s\nZertifikatsfingerabdruck: %s\nZugangstoken (jetzt sicher im Client hinterlegen): %s\n", *configPath, c.Listen, fp, token)
		return nil
	}
	c, err := identity.Load(*configPath)
	if err != nil {
		return err
	}
	switch command {
	case "fingerprint":
		fp, err := identity.Fingerprint(c.Certificate)
		if err == nil {
			fmt.Println(fp)
		}
		return err
	case "rotate-token":
		token, hash, err := identity.NewToken()
		if err != nil {
			return err
		}
		c.TokenHash = hash
		if err = files.Write(*configPath, c); err != nil {
			return err
		}
		fmt.Printf("Neues Zugangstoken: %s\nDienst anschließend neu starten.\n", token)
		return nil
	case "run":
	default:
		return fmt.Errorf("unbekannter Befehl %q (run, init, fingerprint, rotate-token, version)", command)
	}
	if os.Geteuid() != 0 {
		return errors.New("Serverdienst muss für Paketverwaltung und Neustarts als root laufen")
	}
	if err := apt.Available(); err != nil {
		return err
	}
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(c.DataDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("ein ServerUpdater-Dienst verwendet dieses Datenverzeichnis bereits")
	}
	// Bind and validate TLS before starting any package operation or touching job state.
	if _, err = tls.LoadX509KeyPair(c.Certificate, c.PrivateKey); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	s, err := server.New(c.DataDir, version, &apt.Manager{Runner: apt.ExecRunner{}}, slog.Default())
	if err != nil {
		return err
	}
	defer s.Close()
	httpServer := &http.Server{Addr: c.Listen, Handler: server.Handler(s, c.TokenHash), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	s.Start(time.Duration(c.CheckIntervalSeconds) * time.Second)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("HTTPS-Dienst gestartet", "listen", c.Listen, "version", version)
		serveErr <- httpServer.ServeTLS(listener, c.Certificate, c.PrivateKey)
	}()
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("Dienst wird beendet; laufende Paketoperationen werden abgeschlossen")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
