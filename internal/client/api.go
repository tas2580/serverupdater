package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"serverupdater/internal/identity"
	"serverupdater/internal/model"
)

type API struct {
	baseURL, token string
	http           *http.Client
}
type Snapshot struct {
	Status  model.Status
	Metrics model.Metrics
	Scan    model.Scan
	Jobs    []model.Job
}

func ValidateProfile(p model.Profile) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("Servernamen eingeben")
	}
	u, err := url.Parse(p.URL)
	if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return errors.New("Serveradresse muss https://IP-ODER-NAME:8443 sein, ohne Pfad oder Zugangsdaten")
	}
	_, err = identity.NormalizeFingerprint(p.Fingerprint)
	return err
}

func New(p model.Profile, token string) (*API, error) {
	if err := ValidateProfile(p); err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("gültiges Zugangstoken eingeben")
	}
	fp, _ := identity.NormalizeFingerprint(p.Fingerprint)
	expected, _ := hex.DecodeString(fp)
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12,
		// Self-signed certificates are authenticated by a mandatory, out-of-band pin.
		// This callback replaces CA/DNS validation; it never accepts an unpinned peer.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("Server liefert kein Zertifikat")
			}
			cert := state.PeerCertificates[0]
			digest := sha256.Sum256(cert.Raw)
			if subtle.ConstantTimeCompare(digest[:], expected) != 1 {
				return errors.New("Zertifikatsfingerabdruck stimmt nicht überein; Verbindung abgelehnt")
			}
			now := time.Now()
			if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
				return errors.New("Serverzertifikat ist noch nicht oder nicht mehr gültig")
			}
			return nil
		},
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 2}
	return &API{baseURL: strings.TrimRight(p.URL, "/"), token: token, http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *API) Close() { a.http.CloseIdleConnections() }

func (a *API) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", identity.RandomID())
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("Serververbindung: %w", err)
	}
	defer resp.Body.Close()
	const max = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return err
	}
	if len(data) > max {
		return errors.New("Serverantwort überschreitet 8 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e model.Error
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s (HTTP %d)", e.Error, resp.StatusCode)
		}
		return fmt.Errorf("Server antwortet mit HTTP %d", resp.StatusCode)
	}
	if output != nil {
		return json.Unmarshal(data, output)
	}
	return nil
}

func (a *API) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	for _, get := range []struct {
		path string
		dst  any
	}{{"/status", &s.Status}, {"/metrics", &s.Metrics}, {"/updates", &s.Scan}, {"/jobs", &s.Jobs}} {
		if err := a.request(ctx, "GET", get.path, nil, get.dst); err != nil {
			return s, err
		}
	}
	return s, nil
}
func (a *API) Job(ctx context.Context, id string) (model.Job, error) {
	var j model.Job
	err := a.request(ctx, "GET", "/jobs/"+url.PathEscape(id), nil, &j)
	return j, err
}
func (a *API) Check(ctx context.Context) (model.Job, error) {
	var j model.Job
	err := a.request(ctx, "POST", "/check", struct{}{}, &j)
	return j, err
}
func (a *API) Plan(ctx context.Context, packages []string) (model.Job, error) {
	var j model.Job
	err := a.request(ctx, "POST", "/plans", model.PlanRequest{Packages: packages}, &j)
	return j, err
}
func (a *API) Install(ctx context.Context, id string, confirmRemovals bool) (model.Job, error) {
	var j model.Job
	err := a.request(ctx, "POST", "/install", model.InstallRequest{PlanID: id, ConfirmRemovals: confirmRemovals}, &j)
	return j, err
}
func (a *API) Reboot(ctx context.Context, hostname string) (model.Job, error) {
	var j model.Job
	err := a.request(ctx, "POST", "/reboot", model.RebootRequest{ConfirmHostname: hostname}, &j)
	return j, err
}
