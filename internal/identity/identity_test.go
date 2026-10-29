package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProvisioningAndTokenRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "etc", "config.json")
	c, token, err := Init(path, filepath.Join(dir, "data"), ":8443", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyToken(token, c.TokenHash) || VerifyToken("wrong", c.TokenHash) {
		t.Fatal("Tokenprüfung")
	}
	loaded, err := Load(path)
	if err != nil || loaded != c {
		t.Fatalf("Konfiguration: %+v %v", loaded, err)
	}
	fp, err := Fingerprint(c.Certificate)
	if err != nil || len(fp) != 64 {
		t.Fatalf("Fingerabdruck: %s %v", fp, err)
	}
	if _, _, err = Init(path, c.DataDir, c.Listen, 3600); err == nil {
		t.Fatal("bestehende Identität überschrieben")
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{path, c.PrivateKey} {
			st, err := os.Stat(p)
			if err != nil || st.Mode().Perm() != 0600 {
				t.Fatalf("Dateirechte %s: %v %v", p, st, err)
			}
		}
	}
	newToken, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if VerifyToken(token, hash) || !VerifyToken(newToken, hash) {
		t.Fatal("Tokenwechsel")
	}
}
