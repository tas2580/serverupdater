package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"serverupdater/internal/model"
)

func TestTokensUseVaultAndNeverProfileFile(t *testing.T) {
	keyring.MockInit()
	v := NewVault()
	if err := v.Set("profile-one", "secret-admin-token"); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfiles(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	p.Items = append(p.Items, model.Profile{ID: "profile-one", Name: "Testserver", URL: "https://127.0.0.1:8443", Fingerprint: strings.Repeat("a", 64)})
	if err = p.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-admin-token") {
		t.Fatal("Token im Profil gespeichert")
	}
	loaded, err := LoadProfiles(p.Path)
	if err != nil || len(loaded.Items) != 1 {
		t.Fatalf("Profile: %v %v", loaded, err)
	}
	if token, err := NewVault().Get("profile-one"); err != nil || token != "secret-admin-token" {
		t.Fatal("gespeichertes Token nicht gefunden")
	}
	v.Delete("profile-one")
	if _, err = NewVault().Get("profile-one"); err == nil {
		t.Fatal("Token nicht entfernt")
	}
}

func TestUnavailableVaultKeepsTokenForSessionOnly(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	v := NewVault()
	if err := v.Set("server", "session-token"); err == nil {
		t.Fatal("fehlenden Schlüsselspeicher nicht angezeigt")
	}
	if token, err := v.Get("server"); err != nil || token != "session-token" {
		t.Fatal("Sitzungstoken verloren")
	}
	if _, err := NewVault().Get("server"); err == nil {
		t.Fatal("Token über Sitzung hinaus gespeichert")
	}
}
