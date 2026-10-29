package desktop

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"serverupdater/internal/client"
	"serverupdater/internal/identity"
	"serverupdater/internal/model"
)

func (d *Desktop) editProfile(existing *model.Profile) {
	p := model.Profile{ID: identity.RandomID()}
	name, address, fingerprint := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	token := widget.NewPasswordEntry()
	address.SetPlaceHolder("https://203.0.113.10:8443")
	fingerprint.SetPlaceHolder("SHA-256-Fingerabdruck aus serverupdaterd init")
	if existing != nil {
		p = *existing
		name.SetText(p.Name)
		address.SetText(p.URL)
		fingerprint.SetText(p.Fingerprint)
		token.SetPlaceHolder("Leer lassen, um das vorhandene Token zu behalten")
	}
	name.Validator = func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("Servernamen eingeben")
		}
		for _, other := range d.profiles.Items {
			if other.ID != p.ID && other.Name == strings.TrimSpace(s) {
				return fmt.Errorf("Servername bereits vorhanden")
			}
		}
		return nil
	}
	address.Validator = func(s string) error {
		return client.ValidateProfile(model.Profile{Name: "Server", URL: strings.TrimSpace(s), Fingerprint: strings.Repeat("0", 64)})
	}
	fingerprint.Validator = func(s string) error { _, err := identity.NormalizeFingerprint(s); return err }
	token.Validator = func(s string) error {
		if existing == nil && strings.TrimSpace(s) == "" {
			return fmt.Errorf("Zugangstoken eingeben")
		}
		if strings.ContainsAny(s, "\r\n") {
			return fmt.Errorf("Token darf keine Zeilenumbrüche enthalten")
		}
		return nil
	}
	var form *dialog.FormDialog
	form = dialog.NewForm("Serververbindung", "Speichern", "Abbrechen", []*widget.FormItem{widget.NewFormItem("Name", name), widget.NewFormItem("HTTPS-Adresse", address), widget.NewFormItem("Zertifikatsfingerabdruck", fingerprint), widget.NewFormItem("Zugangstoken", token)}, func(ok bool) {
		if !ok {
			return
		}
		p.Name = strings.TrimSpace(name.Text)
		p.URL = strings.TrimRight(strings.TrimSpace(address.Text), "/")
		p.Fingerprint = strings.TrimSpace(fingerprint.Text)
		if err := client.ValidateProfile(p); err != nil {
			dialog.ShowError(err, d.Window)
			return
		}
		p.Fingerprint, _ = identity.NormalizeFingerprint(p.Fingerprint)
		for _, other := range d.profiles.Items {
			if other.ID != p.ID && other.Name == p.Name {
				dialog.ShowError(fmt.Errorf("Servername %q wird bereits verwendet", p.Name), d.Window)
				return
			}
		}
		value := strings.TrimSpace(token.Text)
		if existing == nil && value == "" {
			dialog.ShowError(fmt.Errorf("Zugangstoken eingeben"), d.Window)
			return
		}
		if strings.ContainsAny(value, "\r\n") {
			dialog.ShowError(fmt.Errorf("Token darf keine Zeilenumbrüche enthalten"), d.Window)
			return
		}
		go func() {
			var warning error
			if value != "" {
				warning = d.secrets.Set(p.ID, value)
			}
			fyne.Do(func() {
				previous := append([]model.Profile(nil), d.profiles.Items...)
				found := false
				for i := range d.profiles.Items {
					if d.profiles.Items[i].ID == p.ID {
						d.profiles.Items[i] = p
						found = true
						break
					}
				}
				if !found {
					d.profiles.Items = append(d.profiles.Items, p)
				}
				if err := d.profiles.Save(); err != nil {
					d.profiles.Items = previous
					dialog.ShowError(err, d.Window)
					return
				}
				d.refreshProfiles(p.ID)
				if warning != nil {
					dialog.ShowInformation("Token für diese Sitzung", warning.Error(), d.Window)
				}
			})
		}()
	}, d.Window)
	form.Resize(fyne.NewSize(800, 340))
	form.Show()
}

func (d *Desktop) removeProfile() {
	if d.profile == nil {
		return
	}
	p := *d.profile
	dialog.ShowConfirm("Serververbindung entfernen?", p.Name+" aus dem Client entfernen? Der Serverdienst und laufende Aufträge bleiben bestehen.", func(ok bool) {
		if !ok {
			return
		}
		previous := append([]model.Profile(nil), d.profiles.Items...)
		filtered := make([]model.Profile, 0, len(previous))
		for _, item := range previous {
			if item.ID != p.ID {
				filtered = append(filtered, item)
			}
		}
		d.profiles.Items = filtered
		if err := d.profiles.Save(); err != nil {
			d.profiles.Items = previous
			dialog.ShowError(err, d.Window)
			return
		}
		d.Disconnect()
		d.profile = nil
		d.busy = false
		d.submitting = false
		d.serverSelect.ClearSelected()
		d.refreshProfiles("")
		d.updates = nil
		d.selected = map[string]bool{}
		d.filterPackages()
		d.system.SetText("Noch keine Systemwerte.")
		d.storage.SetText("Noch keine Speicherwerte.")
		d.scanLabel.SetText("Noch keine Paketprüfung.")
		d.rebootHint.SetText("")
		d.jobID = ""
		d.shownJob = nil
		d.jobSelect.Options = nil
		d.jobSelect.ClearSelected()
		d.jobSelect.Refresh()
		d.jobLog.SetText("")
		d.jobStatus.SetText("Noch keine Aufträge.")
		d.connection.SetText("Server hinzufügen oder auswählen.")
		d.updateButtons()
		go d.secrets.Delete(p.ID)
		if len(filtered) > 0 {
			d.serverSelect.SetSelected(d.profileLabels()[0])
		}
	}, d.Window)
}
