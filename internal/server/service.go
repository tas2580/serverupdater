package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"serverupdater/internal/files"
	"serverupdater/internal/identity"
	"serverupdater/internal/model"
)

type Backend interface {
	Refresh(context.Context, func(string)) ([]model.PackageUpdate, error)
	Plan(context.Context, []string, func(string)) (model.Plan, error)
	Install(context.Context, model.Plan, bool, func(string)) error
	Reboot(context.Context, func(string)) error
}

var ErrBusy = errors.New("Server führt bereits eine Paketoperation oder einen Neustart aus")
var ErrClosed = errors.New("Serverdienst wird beendet")

type Service struct {
	mu            sync.Mutex
	backend       Backend
	dataDir       string
	hostname      string
	bootID        string
	version       string
	jobs          []model.Job
	scan          model.Scan
	metrics       model.Metrics
	active        string
	rebootPending bool
	closed        bool
	dirty         bool
	stop          chan struct{}
	once          sync.Once
	wg            sync.WaitGroup
	logger        *slog.Logger
}

func New(dataDir, version string, backend Backend, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	s := &Service{backend: backend, dataDir: dataDir, version: version, stop: make(chan struct{}), logger: logger, jobs: []model.Job{}, scan: model.Scan{Updates: []model.PackageUpdate{}}}
	s.hostname, _ = os.Hostname()
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	s.bootID = strings.TrimSpace(string(b))
	if err := files.Read(filepath.Join(dataDir, "jobs.json"), &s.jobs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := files.Read(filepath.Join(dataDir, "scan.json"), &s.scan); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for i := range s.jobs {
		if !s.jobs[i].Done() {
			now := time.Now().UTC()
			s.jobs[i].State = "interrupted"
			s.jobs[i].FinishedAt = &now
			s.jobs[i].Error = "Serverdienst wurde unterbrochen. Auftrag wird nicht wiederholt; Paketstatus prüfen."
		}
	}
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) Start(interval time.Duration) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		metricsTick := time.NewTicker(5 * time.Second)
		defer metricsTick.Stop()
		checkTick := time.NewTicker(interval)
		defer checkTick.Stop()
		persistTick := time.NewTicker(time.Second)
		defer persistTick.Stop()
		s.sample()
		_, _ = s.Check("scheduled-" + identity.RandomID())
		for {
			select {
			case <-s.stop:
				return
			case <-metricsTick.C:
				s.sample()
			case <-checkTick.C:
				if _, err := s.Check("scheduled-" + identity.RandomID()); err != nil && !errors.Is(err, ErrBusy) {
					s.logger.Error("Paketprüfung starten", "error", err)
				}
			case <-persistTick.C:
				s.mu.Lock()
				if s.dirty {
					if err := s.persistLocked(); err != nil {
						s.logger.Error("Aufträge speichern", "error", err)
					}
				}
				s.mu.Unlock()
			}
		}
	}()
}

func (s *Service) sample() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	m := Sample(ctx)
	s.mu.Lock()
	s.metrics = m
	s.mu.Unlock()
}

func (s *Service) Close() error {
	s.once.Do(func() { s.mu.Lock(); s.closed = true; close(s.stop); s.mu.Unlock() })
	s.wg.Wait() // Never kill dpkg just because the HTTP server or service is stopping.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func (s *Service) persistLocked() error {
	if err := files.Write(filepath.Join(s.dataDir, "jobs.json"), s.jobs); err != nil {
		return err
	}
	if err := files.Write(filepath.Join(s.dataDir, "scan.json"), s.scan); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func cloneJob(j model.Job) model.Job {
	if j.Plan != nil {
		p := *j.Plan
		p.Selected = append([]string(nil), p.Selected...)
		p.Actions = append([]model.Action(nil), p.Actions...)
		j.Plan = &p
	}
	return j
}

func (s *Service) submit(kind, key, request string, work func(func(string)) (*model.Plan, error)) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(key) < 8 || len(key) > 128 {
		return model.Job{}, errors.New("Idempotency-Key mit 8 bis 128 Zeichen erforderlich")
	}
	digest := sha256.Sum256([]byte(kind + "\n" + request))
	requestHash := hex.EncodeToString(digest[:])
	for _, j := range s.jobs {
		if j.Key == key {
			if j.Kind != kind || j.RequestHash != requestHash {
				return model.Job{}, errors.New("Idempotency-Key bereits für anderen Auftrag verwendet")
			}
			return cloneJob(j), nil
		}
	}
	if s.closed {
		return model.Job{}, ErrClosed
	}
	if s.active != "" || s.rebootPending {
		return model.Job{}, ErrBusy
	}
	j := model.Job{ID: identity.RandomID(), Key: key, Kind: kind, RequestHash: requestHash, State: "running", CreatedAt: time.Now().UTC()}
	if len(s.jobs) >= 100 {
		s.jobs = append([]model.Job(nil), s.jobs[len(s.jobs)-99:]...)
	}
	s.jobs = append(s.jobs, j)
	s.active = j.ID
	if err := s.persistLocked(); err != nil {
		s.jobs = s.jobs[:len(s.jobs)-1]
		s.active = ""
		return model.Job{}, err
	}
	s.wg.Add(1)
	go s.run(j, work)
	return cloneJob(j), nil
}

func (s *Service) run(job model.Job, work func(func(string)) (*model.Plan, error)) {
	defer s.wg.Done()
	emit := func(message string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := range s.jobs {
			if s.jobs[i].ID == job.ID {
				const maxLog = 256 << 10
				j := &s.jobs[i]
				j.Log += message
				if len(j.Log) > maxLog {
					j.Log = "[Ältere Ausgabe gekürzt]\n" + j.Log[len(j.Log)-maxLog:]
				}
				s.dirty = true
				break
			}
		}
	}
	s.logger.Info("Auftrag gestartet", "id", job.ID, "kind", job.Kind)
	plan, err := work(emit)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == job.ID {
			j := &s.jobs[i]
			now := time.Now().UTC()
			j.FinishedAt = &now
			j.Plan = plan
			if err != nil {
				j.State = "failed"
				j.Error = err.Error()
			} else {
				j.State = "succeeded"
			}
			break
		}
	}
	if job.Kind == "reboot" && err == nil {
		s.rebootPending = true
	}
	s.active = ""
	s.dirty = true
	if saveErr := s.persistLocked(); saveErr != nil {
		s.logger.Error("Auftrag speichern", "error", saveErr)
	}
	s.logger.Info("Auftrag beendet", "id", job.ID, "error", err)
}

func (s *Service) Check(key string) (model.Job, error) {
	return s.submit("check", key, "", func(emit func(string)) (*model.Plan, error) {
		return nil, s.refresh(emit)
	})
}

func (s *Service) refresh(emit func(string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	attempt := time.Now().UTC()
	updates, err := s.backend.Refresh(ctx, emit)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scan.LastAttempt = attempt
	if err != nil {
		s.scan.Error = err.Error()
	} else {
		s.scan.Error = ""
		s.scan.CheckedAt = time.Now().UTC()
		s.scan.Updates = updates
	}
	s.dirty = true
	return err
}

func (s *Service) CreatePlan(key string, packages []string) (model.Job, error) {
	selected := append([]string(nil), packages...)
	sort.Strings(selected)
	return s.submit("plan", key, strings.Join(selected, "\n"), func(emit func(string)) (*model.Plan, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := s.backend.Plan(ctx, selected, emit)
		if err != nil {
			return nil, err
		}
		return &p, nil
	})
}

func (s *Service) Install(key, planID string, confirmRemovals bool) (model.Job, error) {
	p, ok := s.Plan(planID)
	if !ok {
		return model.Job{}, errors.New("Vorschau nicht gefunden; bitte erneut erstellen")
	}
	if p.HasRemovals() && !confirmRemovals {
		return model.Job{}, errors.New("Diese Vorschau enthält Paketentfernungen. Bitte im aktuellen Client ausdrücklich bestätigen.")
	}
	return s.submit("install", key, fmt.Sprintf("%s:%t", planID, confirmRemovals), func(emit func(string)) (*model.Plan, error) {
		err := s.backend.Install(context.Background(), p, confirmRemovals, emit)
		emit("\nPaketliste nach Installation neu prüfen …\n")
		refreshErr := s.refresh(emit)
		if refreshErr != nil {
			emit("Paketprüfung fehlgeschlagen: " + refreshErr.Error() + "\n")
		}
		return nil, errors.Join(err, refreshErr)
	})
}

func (s *Service) Reboot(key, confirmHostname string) (model.Job, error) {
	if confirmHostname == "" || confirmHostname != s.hostname {
		return model.Job{}, errors.New("Servername für Neustartbestätigung stimmt nicht überein")
	}
	return s.submit("reboot", key, confirmHostname, func(emit func(string)) (*model.Plan, error) {
		emit("Neustart wird in fünf Sekunden über systemd ausgelöst.\n")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return nil, s.backend.Reboot(ctx, emit)
	})
}

func (s *Service) Plan(id string) (model.Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.State == "succeeded" && j.Plan != nil && j.Plan.ID == id {
			return *cloneJob(j).Plan, true
		}
	}
	return model.Plan{}, false
}
func (s *Service) Jobs() []model.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]model.Job, len(s.jobs))
	for i, j := range s.jobs {
		j.Log = ""
		j.Plan = nil
		jobs[i] = j
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs
}
func (s *Service) Job(id string) (model.Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id {
			return cloneJob(j), true
		}
	}
	return model.Job{}, false
}
func (s *Service) Scan() model.Scan {
	s.mu.Lock()
	defer s.mu.Unlock()
	scan := s.scan
	scan.Updates = append([]model.PackageUpdate{}, scan.Updates...)
	return scan
}
func (s *Service) Metrics() model.Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.metrics
	m.Disks = append([]model.Disk{}, m.Disks...)
	m.Errors = append([]string(nil), m.Errors...)
	return m
}
func (s *Service) Status() model.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := os.Stat("/run/reboot-required")
	hint := "Kein Neustarthinweis vorhanden; ein nötiger Neustart ist damit nicht ausgeschlossen."
	if err == nil {
		hint = "Das System meldet einen erforderlichen Neustart."
	}
	if s.rebootPending {
		hint = "Neustart beauftragt; warte auf neue Verbindung."
	}
	return model.Status{Version: s.version, Hostname: s.hostname, BootID: s.bootID, PackageBusy: s.active != "" || s.rebootPending, ActiveJobID: s.active, RebootRequired: err == nil, RebootHint: hint}
}
