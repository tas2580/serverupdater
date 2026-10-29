package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"serverupdater/internal/files"
	"serverupdater/internal/model"
)

type fakeBackend struct {
	release    chan struct{}
	started    chan struct{}
	refreshErr error
	reboots    atomic.Int32
}

func (b *fakeBackend) Refresh(ctx context.Context, emit func(string)) ([]model.PackageUpdate, error) {
	if b.started != nil {
		select {
		case b.started <- struct{}{}:
		default:
		}
	}
	if b.release != nil {
		<-b.release
	}
	emit("Geprüft\n")
	return []model.PackageUpdate{{ID: "foo:amd64", Installed: "1", Candidate: "2"}}, b.refreshErr
}
func (b *fakeBackend) Plan(ctx context.Context, p []string, emit func(string)) (model.Plan, error) {
	return model.Plan{ID: "plan-one", Selected: p, Actions: []model.Action{{Package: "foo:amd64", From: "1", To: "2"}}, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (b *fakeBackend) Install(context.Context, model.Plan, func(string)) error { return nil }
func (b *fakeBackend) Reboot(context.Context, func(string)) error              { b.reboots.Add(1); return nil }
func newTestService(t *testing.T, b Backend) *Service {
	t.Helper()
	s, err := New(t.TempDir(), "test", b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func await(t *testing.T, s *Service, id string) model.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := s.Job(id)
		if j.Done() {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Auftrag läuft zu lange")
	return model.Job{}
}

func TestIndependentRebootAndConfirmation(t *testing.T) {
	b := &fakeBackend{}
	s := newTestService(t, b)
	if _, err := s.Reboot("reboot-invalid", "anderer-server"); err == nil {
		t.Fatal("falscher Hostname angenommen")
	}
	if b.reboots.Load() != 0 {
		t.Fatal("Neustart ohne Bestätigung")
	}
	j, err := s.Reboot("reboot-confirmed", s.Status().Hostname)
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, s, j.ID); got.State != "succeeded" {
		t.Fatalf("Auftrag: %+v", got)
	}
	if b.reboots.Load() != 1 || !s.Scan().CheckedAt.IsZero() {
		t.Fatal("Neustart muss ohne Paketprüfung funktionieren")
	}
	duplicate, err := s.Reboot("reboot-confirmed", s.Status().Hostname)
	if err != nil || duplicate.ID != j.ID || b.reboots.Load() != 1 {
		t.Fatal("doppelter Neustart")
	}
	if _, err = s.Check("check-after-reboot"); !errors.Is(err, ErrBusy) {
		t.Fatal("neue Operation trotz ausstehendem Neustart")
	}
}

func TestPackageOperationBlocksRebootAndSurvivesDisconnect(t *testing.T) {
	b := &fakeBackend{release: make(chan struct{}), started: make(chan struct{}, 1)}
	s := newTestService(t, b)
	var release sync.Once
	defer release.Do(func() { close(b.release) })
	j, err := s.Check("check-blocking")
	if err != nil {
		t.Fatal(err)
	}
	<-b.started
	if _, err = s.Reboot("reboot-blocked", s.Status().Hostname); !errors.Is(err, ErrBusy) {
		t.Fatal("Neustart während Paketoperation zugelassen")
	}
	duplicate, err := s.Check("check-blocking")
	if err != nil || duplicate.ID != j.ID {
		t.Fatal("Auftrag nicht idempotent")
	}
	if b.reboots.Load() != 0 {
		t.Fatal("Backend-Neustart ausgeführt")
	}
	release.Do(func() { close(b.release) })
	if got := await(t, s, j.ID); got.State != "succeeded" {
		t.Fatalf("Auftrag: %+v", got)
	}
}

func TestFailedScanKeepsPreviousResults(t *testing.T) {
	b := &fakeBackend{}
	s := newTestService(t, b)
	j, err := s.Check("check-success")
	if err != nil {
		t.Fatal(err)
	}
	await(t, s, j.ID)
	previous := s.Scan()
	b.refreshErr = errors.New("Paketquelle offline")
	j, err = s.Check("check-failure")
	if err != nil {
		t.Fatal(err)
	}
	await(t, s, j.ID)
	current := s.Scan()
	if current.Error == "" || !current.CheckedAt.Equal(previous.CheckedAt) || len(current.Updates) != 1 {
		t.Fatalf("alter Stand verloren: %+v", current)
	}
}

func TestRestartMarksInterruptedJobsAndPreservesPlans(t *testing.T) {
	dir := t.TempDir()
	jobs := []model.Job{{ID: "running", Kind: "install", State: "running"}, {ID: "preview", Kind: "plan", State: "succeeded", Plan: &model.Plan{ID: "persisted-plan", Selected: []string{"foo:amd64"}, Actions: []model.Action{{Package: "foo:amd64", From: "1", To: "2"}}}}}
	if err := files.Write(filepath.Join(dir, "jobs.json"), jobs); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, "test", &fakeBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, _ := s.Job("running")
	if j.State != "interrupted" || j.FinishedAt == nil {
		t.Fatalf("nicht als unterbrochen markiert: %+v", j)
	}
	p, ok := s.Plan("persisted-plan")
	if !ok || len(p.Selected) != 1 || p.Actions[0].To != "2" {
		t.Fatal("Vorschau verloren")
	}
}

func TestIdempotencyKeyRejectsDifferentRequests(t *testing.T) {
	s := newTestService(t, &fakeBackend{})
	j, err := s.CreatePlan("same-key-plan", []string{"foo:amd64"})
	if err != nil {
		t.Fatal(err)
	}
	await(t, s, j.ID)
	if _, err = s.CreatePlan("same-key-plan", []string{"bar:amd64"}); err == nil {
		t.Fatal("Schlüssel für anderen Inhalt wiederverwendet")
	}
}
