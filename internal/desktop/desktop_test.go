package desktop

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"serverupdater/internal/client"
	"serverupdater/internal/model"
)

type noSecrets struct{}

func (noSecrets) Get(string) (string, error) { return "test", nil }
func (noSecrets) Set(string, string) error   { return nil }
func (noSecrets) Delete(string)              {}

type noRemote struct{}

func (noRemote) Snapshot(ctx context.Context) (client.Snapshot, error) {
	<-ctx.Done()
	return client.Snapshot{}, ctx.Err()
}
func (noRemote) Job(ctx context.Context, _ string) (model.Job, error) {
	<-ctx.Done()
	return model.Job{}, ctx.Err()
}
func (noRemote) Check(context.Context) (model.Job, error)           { panic("unerwarteter Start") }
func (noRemote) Plan(context.Context, []string) (model.Job, error)  { panic("unerwarteter Start") }
func (noRemote) Install(context.Context, string) (model.Job, error) { panic("unerwarteter Start") }
func (noRemote) Reboot(context.Context, string) (model.Job, error)  { panic("unerwarteter Neustart") }
func (noRemote) Close()                                             {}

func newDesktop(t *testing.T) *Desktop {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	d := NewWithFactory(a, &client.Profiles{Path: filepath.Join(t.TempDir(), "profiles.json")}, noSecrets{}, func(model.Profile, string) (Remote, error) { return noRemote{}, nil })
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.remote = noRemote{}
	d.profile = &model.Profile{ID: "one", Name: "Produktionsserver", URL: "https://203.0.113.10:8443"}
	d.Window.Show()
	t.Cleanup(d.Disconnect)
	return d
}
func snapshot() client.Snapshot {
	return client.Snapshot{Status: model.Status{Hostname: "debian-server", BootID: "old-boot", RebootHint: "Kein Neustarthinweis vorhanden."}, Scan: model.Scan{CheckedAt: time.Now(), Updates: []model.PackageUpdate{}}, Metrics: model.Metrics{SampledAt: time.Now(), CPUPercent: 24.5, Load1: 0.48, Load5: 0.32, Load15: 0.25, RAMTotal: 8 << 30, RAMAvailable: 5 << 30, RAMUsedPercent: 37.5, UptimeSeconds: 125643, Disks: []model.Disk{{Mountpoint: "/", Total: 100 << 30, Available: 62 << 30, UsedPercent: 38}, {Mountpoint: "/var", Total: 200 << 30, Available: 145 << 30, UsedPercent: 27.5}}}}
}

func dialogButton(t *testing.T, d *Desktop, text string) *widget.Button {
	t.Helper()
	top := d.Window.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("Bestätigung fehlt")
	}
	pop := top.(*widget.PopUp)
	content := pop.Content.(*fyne.Container)
	buttons := content.Objects[3].(*fyne.Container)
	for _, obj := range buttons.Objects {
		if b, ok := obj.(*widget.Button); ok && b.Text == text {
			return b
		}
	}
	t.Fatalf("Dialogknopf %q fehlt", text)
	return nil
}

func TestRebootWithoutUpdatesRequiresConfirmation(t *testing.T) {
	d := newDesktop(t)
	d.applySnapshot(snapshot())
	if d.rebootButton.Disabled() {
		t.Fatal("Neustartknopf ohne Updates deaktiviert")
	}
	test.Tap(d.rebootButton)
	test.Tap(dialogButton(t, d, "Abbrechen")) // noRemote.Reboot panics if cancellation triggers a request.
	if d.submitting {
		t.Fatal("Neustart trotz Abbruch gestartet")
	}
	d.status.PackageBusy = true
	d.busy = true
	d.updateButtons()
	if !d.rebootButton.Disabled() {
		t.Fatal("Neustart während Paketoperation möglich")
	}
}

func TestServerSwitchInvalidatesOpenConfirmation(t *testing.T) {
	d := newDesktop(t)
	d.applySnapshot(snapshot())
	test.Tap(d.rebootButton)
	d.generation++
	d.profile = &model.Profile{ID: "two", Name: "Anderer Server"}
	test.Tap(dialogButton(t, d, "Neu starten"))
	if d.submitting {
		t.Fatal("alter Bestätigungsdialog startet Aktion auf anderem Server")
	}
}

func TestPackageSelectionAndExpiredPreview(t *testing.T) {
	d := newDesktop(t)
	s := snapshot()
	s.Scan.Updates = []model.PackageUpdate{{ID: "openssl:amd64", Installed: "3.0.1", Candidate: "3.0.2"}, {ID: "linux-image-amd64:amd64", Installed: "6.1", Candidate: "6.2", Held: true}}
	d.applySnapshot(s)
	test.Tap(d.selectAll)
	if len(d.selected) != 1 || !d.selected["openssl:amd64"] {
		t.Fatalf("falsche Auswahl: %v", d.selected)
	}
	row := d.packages.CreateItem().(*fyne.Container)
	d.packages.UpdateItem(0, row)
	if !strings.Contains(row.Objects[0].(*widget.Label).Text, "openssl") {
		t.Fatal("Paketzeile nicht dargestellt")
	}
	d.search.SetText("linux")
	if len(d.visible) != 1 || !d.visible[0].Held {
		t.Fatal("Paketfilter")
	}
	test.Tap(d.selectNone)
	if !d.planButton.Disabled() {
		t.Fatal("leere Auswahl kann installiert werden")
	}
	d.shownJob = &model.Job{Plan: &model.Plan{ExpiresAt: time.Now().Add(-time.Minute)}}
	d.updateButtons()
	if !d.previewButton.Disabled() {
		t.Fatal("abgelaufene Vorschau bestätigt")
	}
}

func TestRebootDetectionAndDashboardPreview(t *testing.T) {
	d := newDesktop(t)
	s := snapshot()
	s.Scan.Updates = []model.PackageUpdate{{ID: "openssl:amd64", Installed: "3.0.16", Candidate: "3.0.17"}, {ID: "curl:amd64", Installed: "8.14.0", Candidate: "8.14.1"}, {ID: "linux-image-amd64:amd64", Installed: "6.12.30", Candidate: "6.12.35", Held: true}}
	d.rebootBootID = "old-boot"
	d.rebootStarted = time.Now()
	s.Status.BootID = "new-boot"
	d.applySnapshot(s)
	if !strings.Contains(d.connection.Text, "Neustart erfolgreich") || d.rebootBootID != "" {
		t.Fatal("Neustart nicht erkannt")
	}
	if d.system.Size().Height < d.system.MinSize().Height {
		t.Fatal("Systemwerte überlappen nach Aktualisierung andere Bedienelemente")
	}
	if output := os.Getenv("SERVERUPDATER_SCREENSHOT"); output != "" {
		f, err := os.Create(output)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err = png.Encode(f, d.Window.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
}
