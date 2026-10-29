package model

import "time"

const APIVersion = "v1"

type Metrics struct {
	SampledAt      time.Time `json:"sampled_at"`
	CPUPercent     float64   `json:"cpu_percent"`
	Load1          float64   `json:"load_1"`
	Load5          float64   `json:"load_5"`
	Load15         float64   `json:"load_15"`
	RAMTotal       uint64    `json:"ram_total"`
	RAMAvailable   uint64    `json:"ram_available"`
	RAMUsedPercent float64   `json:"ram_used_percent"`
	UptimeSeconds  uint64    `json:"uptime_seconds"`
	Disks          []Disk    `json:"disks"`
	Errors         []string  `json:"errors,omitempty"`
}

type Disk struct {
	Mountpoint  string  `json:"mountpoint"`
	Filesystem  string  `json:"filesystem"`
	Total       uint64  `json:"total"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"used_percent"`
}

type PackageUpdate struct {
	ID        string `json:"id"`
	Installed string `json:"installed"`
	Candidate string `json:"candidate"`
	Held      bool   `json:"held"`
}

type Scan struct {
	CheckedAt   time.Time       `json:"checked_at"`
	LastAttempt time.Time       `json:"last_attempt"`
	Error       string          `json:"error,omitempty"`
	Updates     []PackageUpdate `json:"updates"`
}

type Action struct {
	Package string `json:"package"`
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
}

type Plan struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Selected  []string  `json:"selected"`
	Actions   []Action  `json:"actions"`
	StateHash string    `json:"state_hash"`
}

// Jobs survive client disconnects. Queued/running jobs are interrupted on daemon restart.
type Job struct {
	ID          string     `json:"id"`
	Key         string     `json:"idempotency_key"`
	Kind        string     `json:"kind"`
	RequestHash string     `json:"request_hash"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Log         string     `json:"log"`
	Error       string     `json:"error,omitempty"`
	Plan        *Plan      `json:"plan,omitempty"`
}

func (j Job) Done() bool {
	return j.State == "succeeded" || j.State == "failed" || j.State == "interrupted"
}

type Status struct {
	Version        string `json:"version"`
	Hostname       string `json:"hostname"`
	BootID         string `json:"boot_id"`
	PackageBusy    bool   `json:"package_busy"`
	ActiveJobID    string `json:"active_job_id,omitempty"`
	RebootRequired bool   `json:"reboot_required"`
	RebootHint     string `json:"reboot_hint"`
}

type Error struct {
	Error string `json:"error"`
}
type PlanRequest struct {
	Packages []string `json:"packages"`
}
type InstallRequest struct {
	PlanID string `json:"plan_id"`
}
type RebootRequest struct {
	ConfirmHostname string `json:"confirm_hostname"`
}

type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint"`
}
