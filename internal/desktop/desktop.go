package desktop

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"serverupdater/internal/branding"
	"serverupdater/internal/client"
	"serverupdater/internal/model"
)

type Remote interface {
	Snapshot(context.Context) (client.Snapshot, error)
	Job(context.Context, string) (model.Job, error)
	Check(context.Context) (model.Job, error)
	Plan(context.Context, []string) (model.Job, error)
	Install(context.Context, string) (model.Job, error)
	Reboot(context.Context, string) (model.Job, error)
	Close()
}
type Secrets interface {
	Get(string) (string, error)
	Set(string, string) error
	Delete(string)
}
type Factory func(model.Profile, string) (Remote, error)

type Desktop struct {
	Window                                                                         fyne.Window
	profiles                                                                       *client.Profiles
	secrets                                                                        Secrets
	factory                                                                        Factory
	serverSelect                                                                   *widget.Select
	connection, system, storage, scanLabel, rebootHint, jobStatus                  *widget.Label
	checkButton, planButton, rebootButton, previewButton, editButton, deleteButton *widget.Button
	selectAll, selectNone                                                          *widget.Button
	search                                                                         *widget.Entry
	packages                                                                       *widget.List
	jobSelect                                                                      *widget.Select
	jobLog                                                                         *widget.Entry
	jobScroll                                                                      *container.Scroll
	updates, visible                                                               []model.PackageUpdate
	selected                                                                       map[string]bool
	profile                                                                        *model.Profile
	remote                                                                         Remote
	ctx                                                                            context.Context
	cancel                                                                         context.CancelFunc
	generation                                                                     uint64
	status                                                                         model.Status
	connected, busy, submitting                                                    bool
	jobIDs                                                                         []string
	jobID                                                                          string
	shownJob                                                                       *model.Job
	rebootBootID                                                                   string
	rebootStarted                                                                  time.Time
	previewed                                                                      map[string]bool
}

func New(a fyne.App, p *client.Profiles, secrets Secrets) *Desktop {
	return NewWithFactory(a, p, secrets, func(p model.Profile, token string) (Remote, error) { return client.New(p, token) })
}

func NewWithFactory(a fyne.App, p *client.Profiles, secrets Secrets, factory Factory) *Desktop {
	d := &Desktop{Window: a.NewWindow("ServerUpdater"), profiles: p, secrets: secrets, factory: factory, selected: map[string]bool{}, previewed: map[string]bool{}}
	d.Window.SetIcon(branding.Icon)
	d.Window.Resize(fyne.NewSize(1120, 800))
	d.connection = widget.NewLabel("Server hinzufügen oder auswählen.")
	d.connection.Wrapping = fyne.TextWrapWord
	d.system = widget.NewLabel("Noch keine Systemwerte.")
	d.system.Wrapping = fyne.TextWrapWord
	d.storage = widget.NewLabel("Noch keine Speicherwerte.")
	d.storage.Wrapping = fyne.TextWrapWord
	d.scanLabel = widget.NewLabel("Noch keine Paketprüfung.")
	d.scanLabel.Wrapping = fyne.TextWrapWord
	d.rebootHint = widget.NewLabel("")
	d.rebootHint.Wrapping = fyne.TextWrapWord
	d.serverSelect = widget.NewSelect(nil, func(value string) {
		for i, label := range d.profileLabels() {
			if label == value {
				p := d.profiles.Items[i]
				d.connect(p)
				return
			}
		}
	})
	d.serverSelect.PlaceHolder = "Server auswählen"
	d.editButton = widget.NewButtonWithIcon("Bearbeiten", theme.SettingsIcon(), func() {
		if d.profile != nil {
			p := *d.profile
			d.editProfile(&p)
		}
	})
	d.deleteButton = widget.NewButtonWithIcon("Entfernen", theme.DeleteIcon(), d.removeProfile)
	toolbar := container.NewBorder(nil, nil, widget.NewButtonWithIcon("Server hinzufügen", theme.ContentAddIcon(), func() { d.editProfile(nil) }), container.NewHBox(d.editButton, d.deleteButton), d.serverSelect)
	d.checkButton = widget.NewButtonWithIcon("Pakete prüfen", theme.ViewRefreshIcon(), func() {
		d.submit(func(ctx context.Context, r Remote) (model.Job, error) { return r.Check(ctx) }, false)
	})
	d.planButton = widget.NewButtonWithIcon("Updatevorschau", theme.ConfirmIcon(), d.createPlan)
	d.planButton.Importance = widget.HighImportance
	d.rebootButton = widget.NewButtonWithIcon("Server neu starten", theme.ComputerIcon(), d.confirmReboot)
	d.search = widget.NewEntry()
	d.search.SetPlaceHolder("Pakete suchen …")
	d.search.OnChanged = func(string) { d.filterPackages() }
	d.selectAll = widget.NewButton("Alle auswählen", func() {
		for _, p := range d.visible {
			if !p.Held {
				d.selected[p.ID] = true
			}
		}
		d.packages.Refresh()
		d.updateButtons()
	})
	d.selectNone = widget.NewButton("Auswahl aufheben", func() { d.selected = map[string]bool{}; d.packages.Refresh(); d.updateButtons() })
	d.packages = widget.NewList(func() int { return len(d.visible) }, func() fyne.CanvasObject {
		label := widget.NewLabel("")
		label.TextStyle.Monospace = true
		return container.NewBorder(nil, nil, widget.NewCheck("", nil), nil, label)
	}, func(id widget.ListItemID, obj fyne.CanvasObject) {
		row := obj.(*fyne.Container)
		label := row.Objects[0].(*widget.Label)
		check := row.Objects[1].(*widget.Check)
		p := d.visible[id]
		check.OnChanged = nil
		check.SetChecked(d.selected[p.ID])
		if p.Held {
			check.Disable()
		} else {
			check.Enable()
		}
		check.OnChanged = func(on bool) {
			if on {
				d.selected[p.ID] = true
			} else {
				delete(d.selected, p.ID)
			}
			d.updateButtons()
		}
		text := fmt.Sprintf("%s    %s → %s", p.ID, p.Installed, p.Candidate)
		if p.Held {
			text += "  [zurückgehalten]"
		}
		label.SetText(text)
	})
	d.jobStatus = widget.NewLabel("Noch keine Aufträge.")
	d.jobStatus.Wrapping = fyne.TextWrapWord
	d.jobLog = widget.NewMultiLineEntry()
	d.jobLog.TextStyle.Monospace = true
	d.jobLog.Wrapping = fyne.TextWrapWord
	// Read-only output still allows selecting and copying text.
	d.jobLog.Disable()
	d.jobScroll = container.NewVScroll(d.jobLog)
	d.jobSelect = widget.NewSelect(nil, func(label string) {
		for i, value := range d.jobSelect.Options {
			if label == value && i < len(d.jobIDs) {
				d.jobID = d.jobIDs[i]
				d.fetchJob()
				return
			}
		}
	})
	d.jobSelect.PlaceHolder = "Auftrag auswählen"
	d.previewButton = widget.NewButton("Updatevorschau öffnen", func() {
		if d.shownJob != nil && d.shownJob.Plan != nil {
			d.confirmPlan(*d.shownJob.Plan)
		}
	})
	jobs := container.NewBorder(container.NewVBox(d.jobSelect, d.jobStatus), d.previewButton, nil, nil, d.jobScroll)
	packageTop := container.NewVBox(d.scanLabel, d.search, container.NewHBox(d.selectAll, d.selectNone, layout.NewSpacer(), d.planButton))
	packagePane := container.NewBorder(packageTop, nil, nil, nil, d.packages)
	split := container.NewVSplit(packagePane, jobs)
	split.SetOffset(0.6)
	header := container.NewVBox(toolbar, d.connection, container.NewGridWithColumns(2, widget.NewCard("Auslastung", "", d.system), widget.NewCard("Speicherplatz", "", d.storage)), d.rebootHint, container.NewHBox(d.checkButton, layout.NewSpacer(), d.rebootButton))
	d.Window.SetContent(container.NewBorder(header, nil, nil, nil, split))
	d.Window.SetCloseIntercept(func() { d.Disconnect(); d.Window.SetCloseIntercept(nil); d.Window.Close() })
	d.refreshProfiles("")
	d.updateButtons()
	if len(p.Items) > 0 {
		d.serverSelect.SetSelected(d.profileLabels()[0])
	}
	return d
}

func (d *Desktop) Disconnect() {
	d.generation++
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	if d.remote != nil {
		d.remote.Close()
		d.remote = nil
	}
	d.connected = false
}

func (d *Desktop) profileLabels() []string {
	labels := make([]string, len(d.profiles.Items))
	for i, p := range d.profiles.Items {
		labels[i] = p.Name + " · " + p.URL
	}
	return labels
}
func (d *Desktop) refreshProfiles(id string) {
	d.serverSelect.Options = d.profileLabels()
	d.serverSelect.Refresh()
	for i, p := range d.profiles.Items {
		if p.ID == id {
			d.serverSelect.ClearSelected()
			d.serverSelect.SetSelected(d.serverSelect.Options[i])
			return
		}
	}
}

func (d *Desktop) connect(p model.Profile) {
	d.Disconnect()
	d.profile = &p
	gen := d.generation
	d.ctx, d.cancel = context.WithCancel(context.Background())
	ctx := d.ctx
	d.connected = false
	d.busy = false
	d.submitting = false
	d.status = model.Status{}
	d.rebootBootID = ""
	d.rebootStarted = time.Time{}
	d.selected = map[string]bool{}
	d.updates = nil
	d.visible = nil
	d.packages.Refresh()
	d.jobID = ""
	d.shownJob = nil
	d.jobIDs = nil
	d.jobSelect.Options = nil
	d.jobSelect.ClearSelected()
	d.jobSelect.Refresh()
	d.jobStatus.SetText("Noch keine Aufträge geladen.")
	d.jobLog.SetText("")
	d.system.SetText("Noch keine Systemwerte.")
	d.storage.SetText("Noch keine Speicherwerte.")
	d.scanLabel.SetText("Paketliste wird geladen.")
	d.rebootHint.SetText("")
	d.connection.SetText("Verbinde mit " + p.Name + " …")
	d.updateButtons()
	d.Window.Content().Refresh()
	go func() {
		token, err := d.secrets.Get(p.ID)
		if err != nil {
			fyne.Do(func() {
				if gen == d.generation {
					d.connection.SetText(err.Error())
				}
			})
			return
		}
		r, err := d.factory(p, token)
		fyne.Do(func() {
			if gen != d.generation {
				if r != nil {
					r.Close()
				}
				return
			}
			if err != nil {
				d.connection.SetText(err.Error())
				return
			}
			d.remote = r
			go d.poll(ctx, gen, r)
		})
	}()
}

func (d *Desktop) poll(ctx context.Context, gen uint64, r Remote) {
	for {
		callCtx, cancel := context.WithTimeout(ctx, 14*time.Second)
		snapshot, err := r.Snapshot(callCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if gen != d.generation {
				return
			}
			if err != nil {
				d.connected = false
				d.connection.SetText("Verbindung unterbrochen: " + err.Error() + " — erneuter Versuch folgt.")
				d.updateButtons()
				d.Window.Content().Refresh()
				return
			}
			d.applySnapshot(snapshot)
		})
		delay := 5 * time.Second
		if err == nil && snapshot.Status.PackageBusy {
			delay = time.Second
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (d *Desktop) applySnapshot(s client.Snapshot) {
	d.connected = true
	d.status = s.Status
	d.busy = s.Status.PackageBusy
	connection := "Verbunden mit " + d.profile.Name + " (" + s.Status.Hostname + ")"
	if d.rebootBootID != "" {
		if s.Status.BootID != "" && s.Status.BootID != d.rebootBootID {
			connection += " — Neustart erfolgreich"
			d.rebootBootID = ""
			d.rebootStarted = time.Time{}
		}
		if d.rebootBootID != "" {
			connection += " — Neustart beauftragt, warte auf neuen Systemstart"
			if time.Since(d.rebootStarted) > 3*time.Minute {
				connection += " (noch nicht bestätigt)"
			}
		}
	}
	d.connection.SetText(connection)
	m := s.Metrics
	if m.SampledAt.IsZero() {
		d.system.SetText("Messwerte werden erfasst …")
	} else {
		d.system.SetText(fmt.Sprintf("CPU: %.1f %%\nLoad: %.2f / %.2f / %.2f\nRAM: %.1f %% — %s verfügbar von %s\nLaufzeit: %s\nStand: %s", m.CPUPercent, m.Load1, m.Load5, m.Load15, m.RAMUsedPercent, bytesText(m.RAMAvailable), bytesText(m.RAMTotal), uptimeText(m.UptimeSeconds), m.SampledAt.Local().Format("15:04:05")))
	}
	lines := make([]string, 0, len(m.Disks))
	for _, disk := range m.Disks {
		lines = append(lines, fmt.Sprintf("%s: %s verfügbar von %s (%.1f %% belegt)", disk.Mountpoint, bytesText(disk.Available), bytesText(disk.Total), disk.UsedPercent))
	}
	if len(m.Errors) > 0 {
		lines = append(lines, "Messfehler: "+strings.Join(m.Errors, "; "))
	}
	d.storage.SetText(strings.Join(lines, "\n"))
	d.rebootHint.SetText(s.Status.RebootHint)
	d.updates = s.Scan.Updates
	valid := map[string]bool{}
	held := 0
	for _, p := range d.updates {
		if !p.Held {
			valid[p.ID] = true
		} else {
			held++
		}
	}
	for id := range d.selected {
		if !valid[id] {
			delete(d.selected, id)
		}
	}
	d.filterPackages()
	scanText := fmt.Sprintf("%d Updates verfügbar, davon %d zurückgehalten", len(d.updates), held)
	if s.Scan.CheckedAt.IsZero() {
		scanText = "Noch keine erfolgreiche Paketprüfung."
	} else {
		scanText += " · geprüft " + s.Scan.CheckedAt.Local().Format("02.01.2006 15:04:05")
	}
	if s.Scan.Error != "" {
		scanText += "\nVERALTETER STAND — Prüfung fehlgeschlagen: " + s.Scan.Error
	}
	d.scanLabel.SetText(scanText)
	d.applyJobs(s.Jobs, s.Status.ActiveJobID)
	d.updateButtons()
	d.Window.Content().Refresh()
}

func (d *Desktop) filterPackages() {
	query := strings.ToLower(strings.TrimSpace(d.search.Text))
	d.visible = nil
	for _, p := range d.updates {
		if strings.Contains(strings.ToLower(p.ID), query) {
			d.visible = append(d.visible, p)
		}
	}
	d.packages.Refresh()
	d.updateButtons()
	d.Window.Content().Refresh()
}
func (d *Desktop) updateButtons() {
	ready := d.connected && d.remote != nil && !d.busy && !d.submitting
	if ready {
		d.checkButton.Enable()
		d.rebootButton.Enable()
		d.selectAll.Enable()
		d.selectNone.Enable()
	} else {
		d.checkButton.Disable()
		d.rebootButton.Disable()
		d.selectAll.Disable()
		d.selectNone.Disable()
	}
	if ready && len(d.selected) > 0 {
		d.planButton.Enable()
		d.planButton.SetText(fmt.Sprintf("Updatevorschau (%d)", len(d.selected)))
	} else {
		d.planButton.Disable()
		d.planButton.SetText("Updatevorschau")
	}
	if d.profile != nil {
		d.editButton.Enable()
		d.deleteButton.Enable()
	} else {
		d.editButton.Disable()
		d.deleteButton.Disable()
	}
	if ready && d.shownJob != nil && d.shownJob.Plan != nil && time.Now().Before(d.shownJob.Plan.ExpiresAt) {
		d.previewButton.Enable()
	} else {
		d.previewButton.Disable()
	}
}

func (d *Desktop) applyJobs(jobs []model.Job, active string) {
	labels := make([]string, 0, len(jobs))
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		labels = append(labels, fmt.Sprintf("%s · %s · %s · %.8s", j.CreatedAt.Local().Format("02.01. 15:04:05"), kindText(j.Kind), stateText(j.State), j.ID))
		ids = append(ids, j.ID)
	}
	d.jobIDs = ids
	d.jobSelect.Options = labels
	d.jobSelect.Refresh()
	if d.jobID == "" {
		d.jobID = active
		if d.jobID == "" && len(ids) > 0 {
			d.jobID = ids[0]
		}
	}
	for i, id := range ids {
		if id == d.jobID {
			if d.jobSelect.Selected != labels[i] {
				d.jobSelect.SetSelected(labels[i])
			} else {
				d.fetchJob()
			}
			return
		}
	}
}
func (d *Desktop) fetchJob() {
	if d.remote == nil || d.jobID == "" {
		return
	}
	r, gen, id, ctx := d.remote, d.generation, d.jobID, d.ctx
	go func() {
		j, err := r.Job(ctx, id)
		fyne.Do(func() {
			if gen != d.generation || id != d.jobID || err != nil {
				return
			}
			d.displayJob(j)
		})
	}()
}
func (d *Desktop) displayJob(j model.Job) {
	d.shownJob = &j
	text := kindText(j.Kind) + ": " + stateText(j.State)
	if j.Error != "" {
		text += "\n" + j.Error
	}
	d.jobStatus.SetText(text)
	if d.jobLog.Text != j.Log {
		d.jobLog.SetText(j.Log)
		d.jobScroll.ScrollToBottom()
	}
	d.updateButtons()
	d.Window.Content().Refresh()
}

func (d *Desktop) submit(start func(context.Context, Remote) (model.Job, error), showPlan bool) {
	if !d.connected || d.remote == nil || d.busy || d.submitting {
		return
	}
	d.submitting = true
	d.updateButtons()
	r, ctx, gen := d.remote, d.ctx, d.generation
	go func() {
		j, err := start(ctx, r)
		fyne.Do(func() {
			if gen != d.generation {
				return
			}
			d.submitting = false
			if err != nil {
				d.updateButtons()
				dialog.ShowError(fmt.Errorf("%w\nBei einem Verbindungsfehler vor erneutem Start die Auftragsliste prüfen.", err), d.Window)
				return
			}
			d.busy = !j.Done()
			d.jobID = j.ID
			d.displayJob(j)
			if j.Kind == "reboot" {
				d.rebootBootID = d.status.BootID
				d.rebootStarted = time.Now()
			}
			go d.watchJob(ctx, gen, r, j.ID, showPlan)
		})
	}()
}
func (d *Desktop) watchJob(ctx context.Context, gen uint64, r Remote, id string, showPlan bool) {
	for {
		j, err := r.Job(ctx, id)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			fyne.Do(func() {
				if gen != d.generation {
					return
				}
				if d.jobID == id {
					d.displayJob(j)
				}
				if j.Done() {
					if j.Kind != "reboot" || j.State != "succeeded" {
						d.busy = false
					}
					if j.Kind == "reboot" && j.State != "succeeded" {
						d.rebootBootID = ""
					}
					d.updateButtons()
					if showPlan && j.State == "succeeded" && j.Plan != nil && !d.previewed[j.Plan.ID] {
						d.previewed[j.Plan.ID] = true
						d.confirmPlan(*j.Plan)
					}
				}
			})
			if j.Done() {
				return
			}
		}
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
func (d *Desktop) createPlan() {
	selected := make([]string, 0, len(d.selected))
	for id := range d.selected {
		selected = append(selected, id)
	}
	sort.Strings(selected)
	d.submit(func(ctx context.Context, r Remote) (model.Job, error) { return r.Plan(ctx, selected) }, true)
}
func (d *Desktop) confirmPlan(p model.Plan) {
	if !d.connected || d.busy || time.Now().After(p.ExpiresAt) {
		dialog.ShowInformation("Vorschau nicht verfügbar", "Bitte eine neue Updatevorschau erstellen.", d.Window)
		return
	}
	gen := d.generation
	lines := []string{"Server: " + d.profile.Name + " (" + d.status.Hostname + ")", "", "Diese vollständige Liste enthält auch benötigte Abhängigkeiten:", ""}
	for _, a := range p.Actions {
		from := a.From
		if from == "" {
			from = "NEU"
		}
		lines = append(lines, fmt.Sprintf("%s: %s → %s", a.Package, from, a.To))
	}
	lines = append(lines, "", "Bestehende Paketkonfigurationen bleiben erhalten.", "Dienste können während des Updates neu starten.", "Der Server wird nicht automatisch neu gestartet.", "Gültig bis "+p.ExpiresAt.Local().Format("15:04:05"))
	text := widget.NewLabel(strings.Join(lines, "\n"))
	text.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(text)
	scroll.SetMinSize(fyne.NewSize(760, 380))
	confirmation := dialog.NewCustomConfirm("Updates installieren?", "Jetzt installieren", "Abbrechen", scroll, func(ok bool) {
		if !ok || gen != d.generation {
			return
		}
		d.submit(func(ctx context.Context, r Remote) (model.Job, error) { return r.Install(ctx, p.ID) }, false)
	}, d.Window)
	confirmation.Show()
}
func (d *Desktop) confirmReboot() {
	if !d.connected || d.busy || d.profile == nil {
		return
	}
	gen, hostname := d.generation, d.status.Hostname
	confirmation := dialog.NewConfirm("Server neu starten?", fmt.Sprintf("%s (%s) jetzt neu starten?\n\nDies funktioniert unabhängig von Updates. Die Verbindung wird während des Neustarts unterbrochen.", d.profile.Name, hostname), func(ok bool) {
		if !ok || gen != d.generation {
			return
		}
		d.submit(func(ctx context.Context, r Remote) (model.Job, error) { return r.Reboot(ctx, hostname) }, false)
	}, d.Window)
	confirmation.SetConfirmText("Neu starten")
	confirmation.SetDismissText("Abbrechen")
	confirmation.Show()
}

func bytesText(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
func uptimeText(seconds uint64) string {
	return fmt.Sprintf("%d Tage %02d:%02d", seconds/86400, (seconds%86400)/3600, (seconds%3600)/60)
}
func kindText(s string) string {
	switch s {
	case "check":
		return "Paketprüfung"
	case "plan":
		return "Updatevorschau"
	case "install":
		return "Installation"
	case "reboot":
		return "Neustart"
	}
	return s
}
func stateText(s string) string {
	switch s {
	case "running":
		return "läuft"
	case "succeeded":
		return "erfolgreich"
	case "failed":
		return "fehlgeschlagen"
	case "interrupted":
		return "unterbrochen"
	}
	return s
}
