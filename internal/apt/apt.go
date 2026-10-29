package apt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"serverupdater/internal/identity"
	"serverupdater/internal/model"
)

type Manager struct{ Runner Runner }
type installed struct {
	ID, Version string
	Held        bool
	Candidate   string
}
type inventory struct {
	Native   string
	Packages map[string]installed
	Hash     string
}

func (i inventory) resolve(id string) string {
	if strings.Contains(id, ":") {
		return id
	}
	if _, ok := i.Packages[id+":"+i.Native]; ok {
		return id + ":" + i.Native
	}
	if _, ok := i.Packages[id+":all"]; ok {
		return id + ":all"
	}
	return id + ":" + i.Native
}

var packageID = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+:[a-z0-9][a-z0-9-]*$`)
var instLine = regexp.MustCompile(`^Inst ([^ ]+)(?: \[([^\]]+)\])? \(([^ )]+)(?: .*)?\)$`)
var archSuffix = regexp.MustCompile(`\[([a-z0-9][a-z0-9-]*)\]\)$`)

func (m *Manager) inventory(ctx context.Context) (inventory, error) {
	i := inventory{Packages: make(map[string]installed)}
	out, err := m.Runner.Run(ctx, []string{"/usr/bin/dpkg", "--print-architecture"}, nil)
	if err != nil {
		return i, err
	}
	i.Native = strings.TrimSpace(string(out))
	out, err = m.Runner.Run(ctx, []string{"/usr/bin/dpkg-query", "-W", "-f=${Package}\t${Architecture}\t${Version}\t${db:Status-Status}\n"}, nil)
	if err != nil {
		return i, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return i, fmt.Errorf("unbekannte dpkg-Ausgabe: %q", line)
		}
		if parts[3] != "installed" {
			continue
		}
		id := parts[0] + ":" + parts[1]
		if !packageID.MatchString(id) || parts[2] == "" {
			return i, fmt.Errorf("ungültiges installiertes Paket: %q", id)
		}
		i.Packages[id] = installed{ID: id, Version: parts[2]}
	}
	if len(i.Packages) == 0 {
		return i, errors.New("keine installierten Pakete gefunden")
	}
	out, err = m.Runner.Run(ctx, []string{"/usr/bin/apt-mark", "showhold"}, nil)
	if err != nil {
		return i, err
	}
	for _, id := range strings.Fields(string(out)) {
		id = i.resolve(id)
		if p, ok := i.Packages[id]; ok {
			p.Held = true
			i.Packages[id] = p
		}
	}
	ids := make([]string, 0, len(i.Packages))
	for id := range i.Packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		args := append([]string{"/usr/bin/apt-cache", "policy"}, ids[start:end]...)
		out, err = m.Runner.Run(ctx, args, nil)
		if err != nil {
			return i, err
		}
		current := ""
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" && line[0] != ' ' && line[0] != '\t' && strings.HasSuffix(line, ":") {
				current = strings.TrimSuffix(line, ":")
				current = i.resolve(current)
			}
			if strings.HasPrefix(strings.TrimSpace(line), "Candidate:") {
				p, ok := i.Packages[current]
				if !ok {
					return i, fmt.Errorf("unbekanntes Paket in apt-cache: %s", current)
				}
				p.Candidate = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Candidate:"))
				if p.Candidate == "(none)" {
					p.Candidate = ""
				}
				i.Packages[current] = p
			}
		}
	}
	// Include candidates and holds: a refresh or external admin action invalidates previews.
	ordered := make([]installed, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, i.Packages[id])
	}
	b, _ := json.Marshal(ordered)
	h := sha256.Sum256(b)
	i.Hash = hex.EncodeToString(h[:])
	return i, nil
}

func (m *Manager) newer(ctx context.Context, old, candidate string) (bool, error) {
	if candidate == "" || old == candidate {
		return false, nil
	}
	_, err := m.Runner.Run(ctx, []string{"/usr/bin/dpkg", "--compare-versions", old, "lt", candidate}, nil)
	if isExitOne(err) {
		return false, nil
	}
	return err == nil, err
}

func (m *Manager) scan(ctx context.Context, i inventory) ([]model.PackageUpdate, error) {
	updates := make([]model.PackageUpdate, 0)
	for _, p := range i.Packages {
		newer, err := m.newer(ctx, p.Version, p.Candidate)
		if err != nil {
			return nil, err
		}
		if newer {
			updates = append(updates, model.PackageUpdate{ID: p.ID, Installed: p.Version, Candidate: p.Candidate, Held: p.Held})
		}
	}
	sort.Slice(updates, func(a, b int) bool { return updates[a].ID < updates[b].ID })
	return updates, nil
}

func (m *Manager) Refresh(ctx context.Context, emit func(string)) ([]model.PackageUpdate, error) {
	_, err := m.Runner.Run(ctx, []string{"/usr/bin/apt-get", "-o", "APT::Update::Error-Mode=any", "-o", "DPkg::Lock::Timeout=60", "update"}, emit)
	if err != nil {
		return nil, err
	}
	i, err := m.inventory(ctx)
	if err != nil {
		return nil, err
	}
	return m.scan(ctx, i)
}

func (m *Manager) Plan(ctx context.Context, selected []string, emit func(string)) (model.Plan, error) {
	if len(selected) == 0 || len(selected) > 10000 {
		return model.Plan{}, errors.New("mindestens ein und höchstens 10000 Pakete auswählen")
	}
	i, err := m.inventory(ctx)
	if err != nil {
		return model.Plan{}, err
	}
	selected = append([]string(nil), selected...)
	sort.Strings(selected)
	args := make([]string, 0, len(selected))
	for n, id := range selected {
		p, ok := i.Packages[id]
		if !ok || !packageID.MatchString(id) {
			return model.Plan{}, fmt.Errorf("kein installiertes Paket: %s", id)
		}
		if n > 0 && id == selected[n-1] {
			return model.Plan{}, errors.New("doppelte Paketauswahl")
		}
		if p.Held {
			return model.Plan{}, fmt.Errorf("Paket ist zurückgehalten: %s", id)
		}
		newer, err := m.newer(ctx, p.Version, p.Candidate)
		if err != nil {
			return model.Plan{}, err
		}
		if !newer {
			return model.Plan{}, fmt.Errorf("kein Update verfügbar: %s", id)
		}
		args = append(args, id+"="+p.Candidate)
	}
	actions, err := m.simulate(ctx, i, args, emit)
	if err != nil {
		return model.Plan{}, err
	}
	if len(actions) == 0 {
		return model.Plan{}, errors.New("APT hat keine Änderungen geplant")
	}
	now := time.Now().UTC()
	return model.Plan{ID: identity.RandomID(), CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), Selected: selected, Actions: actions, StateHash: i.Hash}, nil
}

func (m *Manager) simulate(ctx context.Context, i inventory, packages []string, emit func(string)) ([]model.Action, error) {
	args := append([]string{"/usr/bin/apt-get", "--simulate", "--no-remove", "--no-install-recommends", "-o", "Dpkg::Use-Pty=0", "install"}, packages...)
	out, err := m.Runner.Run(ctx, args, emit)
	if err != nil {
		return nil, err
	}
	return m.parseActions(ctx, i, string(out))
}

func (m *Manager) parseActions(ctx context.Context, i inventory, out string) ([]model.Action, error) {
	actions := make([]model.Action, 0)
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Remv ") || strings.HasPrefix(line, "Purg ") {
			return nil, errors.New("Update würde Pakete entfernen")
		}
		if !strings.HasPrefix(line, "Inst ") {
			continue
		}
		match := instLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("unbekannte APT-Installationszeile: %q", line)
		}
		id := match[1]
		if !strings.Contains(id, ":") {
			arch := i.Native
			if am := archSuffix.FindStringSubmatch(line); am != nil {
				arch = am[1]
			}
			id += ":" + arch
		}
		if !packageID.MatchString(id) || seen[id] {
			return nil, fmt.Errorf("ungültige APT-Aktion: %s", id)
		}
		seen[id] = true
		p, installed := i.Packages[id]
		if p.Held {
			return nil, fmt.Errorf("Abhängigkeit ist zurückgehalten: %s", id)
		}
		if installed {
			newer, err := m.newer(ctx, p.Version, match[3])
			if err != nil {
				return nil, err
			}
			if !newer {
				return nil, fmt.Errorf("Neuinstallation oder Downgrade wird abgelehnt: %s", id)
			}
		}
		actions = append(actions, model.Action{Package: id, From: p.Version, To: match[3]})
	}
	sort.Slice(actions, func(a, b int) bool { return actions[a].Package < actions[b].Package })
	return actions, nil
}

func (m *Manager) Install(ctx context.Context, plan model.Plan, emit func(string)) error {
	if time.Now().After(plan.ExpiresAt) {
		return errors.New("Vorschau abgelaufen; bitte erneut erstellen und bestätigen")
	}
	i, err := m.inventory(ctx)
	if err != nil {
		return err
	}
	if i.Hash != plan.StateHash {
		return errors.New("Paketstand hat sich geändert; neue Vorschau und Bestätigung erforderlich")
	}
	args := make([]string, 0, len(plan.Selected))
	for _, id := range plan.Selected {
		p, ok := i.Packages[id]
		if !ok || p.Held || !packageID.MatchString(id) {
			return errors.New("Paketauswahl ist nicht mehr gültig")
		}
		args = append(args, id+"="+p.Candidate)
	}
	actions, err := m.simulate(ctx, i, args, emit)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actions, plan.Actions) {
		return errors.New("Abhängigkeiten haben sich geändert; neue Vorschau und Bestätigung erforderlich")
	}
	// Pin every confirmed action so dependency candidates cannot silently drift.
	pinned := make([]string, 0, len(actions))
	auto, err := m.Runner.Run(ctx, []string{"/usr/bin/apt-mark", "showauto"}, nil)
	if err != nil {
		return err
	}
	autoIDs := map[string]bool{}
	for _, id := range strings.Fields(string(auto)) {
		autoIDs[i.resolve(id)] = true
	}
	restoreAuto := make([]string, 0)
	selected := map[string]bool{}
	for _, id := range plan.Selected {
		selected[id] = true
	}
	for _, a := range actions {
		pinned = append(pinned, a.Package+"="+a.To)
		if autoIDs[a.Package] || (a.From == "" && !selected[a.Package]) {
			restoreAuto = append(restoreAuto, a.Package)
		}
	}
	cmd := append([]string{"/usr/bin/apt-get", "--assume-yes", "--no-remove", "--no-install-recommends", "-o", "DPkg::Lock::Timeout=60", "-o", "Dpkg::Use-Pty=0", "-o", "Dpkg::Options::=--force-confold", "install"}, pinned...)
	_, installErr := m.Runner.Run(ctx, cmd, emit)
	// Explicit version arguments can change APT's automatic-install marks; preserve them.
	if len(restoreAuto) > 0 {
		_, markErr := m.Runner.Run(ctx, append([]string{"/usr/bin/apt-mark", "auto"}, restoreAuto...), emit)
		if markErr != nil {
			return errors.Join(installErr, fmt.Errorf("APT-Automatikmarkierungen wiederherstellen: %w", markErr))
		}
	}
	return installErr
}

func (m *Manager) Reboot(ctx context.Context, emit func(string)) error {
	if err := checkSystemPackageLocks(); err != nil {
		return err
	}
	_, err := m.Runner.Run(ctx, []string{"/usr/bin/systemd-run", "--unit=serverupdater-reboot", "--on-active=5s", "/usr/bin/systemctl", "reboot"}, emit)
	return err
}
