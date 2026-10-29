package client_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"serverupdater/internal/client"
	"serverupdater/internal/identity"
	"serverupdater/internal/model"
	"serverupdater/internal/server"
)

type backend struct {
	reboots atomic.Int32
	release chan struct{}
	entered chan struct{}
}

func (b *backend) Refresh(ctx context.Context, emit func(string)) ([]model.PackageUpdate, error) {
	if b.entered != nil {
		select {
		case b.entered <- struct{}{}:
		default:
		}
	}
	if b.release != nil {
		<-b.release
	}
	emit("APT-Testausgabe\n")
	return []model.PackageUpdate{}, nil
}
func (b *backend) Plan(ctx context.Context, selected []string, emit func(string)) (model.Plan, error) {
	return model.Plan{ID: "test-plan", Selected: selected, ExpiresAt: time.Now().Add(time.Minute), Actions: []model.Action{{Package: "foo:amd64", From: "1", To: "2"}}}, nil
}
func (b *backend) Install(context.Context, model.Plan, func(string)) error { return nil }
func (b *backend) Reboot(context.Context, func(string)) error              { b.reboots.Add(1); return nil }

func setup(t *testing.T, b *backend) (*server.Service, *httptest.Server, model.Profile, string) {
	t.Helper()
	token, hash, err := identity.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.New(t.TempDir(), "test", b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewTLSServer(server.Handler(s, hash))
	t.Cleanup(func() { h.Close(); s.Close() })
	fp := sha256.Sum256(h.Certificate().Raw)
	p := model.Profile{ID: "test", Name: "Testserver", URL: h.URL, Fingerprint: hex.EncodeToString(fp[:])}
	return s, h, p, token
}
func waitJob(t *testing.T, a *client.API, id string) model.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, err := a.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Done() {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Auftrag nicht abgeschlossen")
	return model.Job{}
}

func TestPinnedHTTPSAndIndependentReboot(t *testing.T) {
	b := &backend{}
	_, _, p, token := setup(t, b)
	a, err := client.New(p, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Scan.CheckedAt.IsZero() || len(s.Scan.Updates) != 0 {
		t.Fatal("Testserver darf noch keine Updates geprüft haben")
	}
	j, err := a.Reboot(context.Background(), s.Status.Hostname)
	if err != nil {
		t.Fatal(err)
	}
	if waitJob(t, a, j.ID).State != "succeeded" || b.reboots.Load() != 1 {
		t.Fatal("Neustart ohne Updates fehlgeschlagen")
	}
}

func TestWrongPinAndTokenCannotReboot(t *testing.T) {
	b := &backend{}
	s, _, p, token := setup(t, b)
	wrongPin := p
	wrongPin.Fingerprint = strings.Repeat("0", 64)
	a, err := client.New(wrongPin, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.Reboot(context.Background(), s.Status().Hostname); err == nil {
		t.Fatal("falsches Zertifikat akzeptiert")
	}
	a, err = client.New(p, "wrong-token")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.Reboot(context.Background(), s.Status().Hostname); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("falsches Token akzeptiert: %v", err)
	}
	if b.reboots.Load() != 0 {
		t.Fatal("nicht autorisierter Neustart")
	}
}

func TestAPIUpdateFlowAndReconnect(t *testing.T) {
	b := &backend{}
	_, _, p, token := setup(t, b)
	a, err := client.New(p, token)
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.Plan(context.Background(), []string{"foo:amd64"})
	if err != nil {
		t.Fatal(err)
	}
	preview := waitJob(t, a, j.ID)
	if preview.Plan == nil {
		t.Fatal("keine Vorschau")
	}
	j, err = a.Install(context.Background(), preview.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	reconnected, err := client.New(p, token)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	if got := waitJob(t, reconnected, j.ID); got.State != "succeeded" || !strings.Contains(got.Log, "APT-Testausgabe") {
		t.Fatalf("Ergebnis: %+v", got)
	}
	snapshot, err := reconnected.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range snapshot.Jobs {
		if summary.Log != "" || summary.Plan != nil {
			t.Fatal("Auftragsliste enthält vollständige Protokolle")
		}
	}
}

func TestHTTPRequestCancellationDoesNotCancelWork(t *testing.T) {
	b := &backend{release: make(chan struct{}), entered: make(chan struct{}, 1)}
	s, _, p, token := setup(t, b)
	released := false
	defer func() {
		if !released {
			close(b.release)
		}
	}()
	a, err := client.New(p, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	j, err := a.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-b.entered
	if got, _ := s.Job(j.ID); got.State != "running" {
		t.Fatal("Hintergrundauftrag abgebrochen")
	}
	if _, err = a.Reboot(context.Background(), s.Status().Hostname); err == nil {
		t.Fatal("Neustart während Paketprüfung zugelassen")
	}
	close(b.release)
	released = true
	if waitJob(t, a, j.ID).State != "succeeded" {
		t.Fatal("Auftrag nach Verbindungsabbruch fehlgeschlagen")
	}
}

func TestHTTPSRejectsRedirectsAndBrowserOrigins(t *testing.T) {
	b := &backend{}
	s, h, p, token := setup(t, b)
	req, _ := http.NewRequest("POST", h.URL+"/api/v1/reboot", strings.NewReader(`{"confirm_hostname":"`+s.Status().Hostname+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Idempotency-Key", "browser-origin")
	resp, err := h.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 || b.reboots.Load() != 0 {
		t.Fatal("Browseranfrage zugelassen")
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, h.URL+"/api/v1/status", 302) }))
	defer redirect.Close()
	digest := sha256.Sum256(redirect.Certificate().Raw)
	p.URL = redirect.URL
	p.Fingerprint = hex.EncodeToString(digest[:])
	a, err := client.New(p, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("Weiterleitung verfolgt: %v", err)
	}
}

func TestRejectInvalidJSONAndUnauthenticatedRequests(t *testing.T) {
	b := &backend{}
	s, h, _, token := setup(t, b)
	for _, body := range []string{`null`, `{"confirm_hostname":"wrong"}`, `{"confirm_hostname":"x","command":"reboot"}`, `{} {}`, `{`} {
		req, _ := http.NewRequest("POST", h.URL+"/api/v1/reboot", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "invalid-json-key")
		resp, err := h.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("ungültige Anfrage akzeptiert: %s", body)
		}
	}
	body, _ := json.Marshal(model.RebootRequest{ConfirmHostname: s.Status().Hostname})
	resp, err := h.Client().Post(h.URL+"/api/v1/reboot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || b.reboots.Load() != 0 {
		t.Fatal("Neustart ohne Token")
	}
}

func TestProfileValidation(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1:8443", "https://user:pass@localhost", "https://localhost/path", "https://localhost?token=x", "https://localhost/#fragment", "file:///tmp/test"} {
		p := model.Profile{Name: "Test", URL: address, Fingerprint: strings.Repeat("a", 64)}
		if err := client.ValidateProfile(p); err == nil {
			t.Fatalf("Adresse akzeptiert: %s", address)
		}
	}
	if _, err := client.New(model.Profile{Name: "Test", URL: "https://[::1]:8443", Fingerprint: strings.Repeat("a", 64)}, "token"); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredPinnedCertificateIsRejected(t *testing.T) {
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	original := seed.TLS.Certificates[0]
	seed.Close()
	cert, err := x509.ParseCertificate(original.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	cert.NotBefore = time.Now().Add(-48 * time.Hour)
	cert.NotAfter = time.Now().Add(-24 * time.Hour)
	signer := original.PrivateKey.(crypto.Signer)
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	h := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	h.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: signer}}}
	h.StartTLS()
	defer h.Close()
	digest := sha256.Sum256(der)
	a, err := client.New(model.Profile{Name: "Test", URL: h.URL, Fingerprint: hex.EncodeToString(digest[:])}, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "gültig") {
		t.Fatalf("abgelaufenes Zertifikat angenommen: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("Anfrage vor Zertifikatsprüfung gesendet")
	}
}
