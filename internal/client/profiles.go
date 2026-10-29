package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
	"serverupdater/internal/files"
	"serverupdater/internal/model"
)

type Profiles struct {
	Path  string
	Items []model.Profile
}

func LoadProfiles(path string) (*Profiles, error) {
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "serverupdater", "profiles.json")
	}
	p := &Profiles{Path: path, Items: []model.Profile{}}
	if err := files.Read(path, &p.Items); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("Serverprofile laden: %w", err)
	}
	ids := map[string]bool{}
	for _, profile := range p.Items {
		if err := ValidateProfile(profile); err != nil {
			return nil, err
		}
		if profile.ID == "" || ids[profile.ID] {
			return nil, errors.New("ungültige oder doppelte Profil-ID")
		}
		ids[profile.ID] = true
	}
	return p, nil
}
func (p *Profiles) Save() error { return files.Write(p.Path, p.Items) }

// Vault never falls back to plaintext storage. The session map also works without D-Bus.
type Vault struct {
	mu      sync.Mutex
	session map[string]string
}

func NewVault() *Vault { return &Vault{session: map[string]string{}} }
func (v *Vault) Get(id string) (string, error) {
	v.mu.Lock()
	token := v.session[id]
	v.mu.Unlock()
	if token != "" {
		return token, nil
	}
	token, err := keyring.Get("serverupdater", id)
	if err != nil {
		return "", errors.New("Kein gespeichertes Token verfügbar. Server bearbeiten und Zugangstoken eingeben.")
	}
	v.mu.Lock()
	v.session[id] = token
	v.mu.Unlock()
	return token, nil
}
func (v *Vault) Set(id, token string) error {
	token = strings.TrimSpace(token)
	v.mu.Lock()
	v.session[id] = token
	v.mu.Unlock()
	if err := keyring.Set("serverupdater", id, token); err != nil {
		// Remove an older persisted value if replacing it failed; avoid using a stale token later.
		_ = keyring.Delete("serverupdater", id)
		return errors.New("Der Schlüsselspeicher ist nicht verfügbar. Das Token bleibt nur für diese Sitzung gespeichert.")
	}
	return nil
}
func (v *Vault) Delete(id string) {
	v.mu.Lock()
	delete(v.session, id)
	v.mu.Unlock()
	_ = keyring.Delete("serverupdater", id)
}
