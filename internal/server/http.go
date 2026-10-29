package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"serverupdater/internal/identity"
	"serverupdater/internal/model"
)

func Handler(s *Service, tokenHash string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.Status()) })
	mux.HandleFunc("GET /api/v1/metrics", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.Metrics()) })
	mux.HandleFunc("GET /api/v1/updates", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.Scan()) })
	mux.HandleFunc("GET /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs := s.Jobs()
		respond(w, 200, jobs)
	})
	mux.HandleFunc("GET /api/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, ok := s.Job(r.PathValue("id"))
		if !ok {
			respond(w, 404, model.Error{Error: "Auftrag nicht gefunden"})
			return
		}
		respond(w, 200, j)
	})
	mux.HandleFunc("POST /api/v1/check", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		j, err := s.Check(r.Header.Get("Idempotency-Key"))
		jobResponse(w, j, err)
	})
	mux.HandleFunc("POST /api/v1/plans", func(w http.ResponseWriter, r *http.Request) {
		var req model.PlanRequest
		if !decode(w, r, &req) {
			return
		}
		if len(req.Packages) == 0 || len(req.Packages) > 10000 {
			respond(w, 400, model.Error{Error: "1 bis 10000 Pakete auswählen"})
			return
		}
		j, err := s.CreatePlan(r.Header.Get("Idempotency-Key"), req.Packages)
		jobResponse(w, j, err)
	})
	mux.HandleFunc("POST /api/v1/install", func(w http.ResponseWriter, r *http.Request) {
		var req model.InstallRequest
		if !decode(w, r, &req) {
			return
		}
		j, err := s.Install(r.Header.Get("Idempotency-Key"), req.PlanID)
		jobResponse(w, j, err)
	})
	mux.HandleFunc("POST /api/v1/reboot", func(w http.ResponseWriter, r *http.Request) {
		var req model.RebootRequest
		if !decode(w, r, &req) {
			return
		}
		j, err := s.Reboot(r.Header.Get("Idempotency-Key"), req.ConfirmHostname)
		jobResponse(w, j, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Header.Get("Origin") != "" {
			respond(w, 403, model.Error{Error: "Browserzugriffe sind nicht zugelassen"})
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || !identity.VerifyToken(strings.TrimPrefix(auth, "Bearer "), tokenHash) {
			respond(w, 401, model.Error{Error: "Zugangstoken fehlt oder ist ungültig"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		respond(w, 400, model.Error{Error: "Ungültige JSON-Anfrage: " + err.Error()})
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		respond(w, 400, model.Error{Error: "Anfrage muss genau ein JSON-Objekt enthalten"})
		return false
	}
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '{' {
		respond(w, 400, model.Error{Error: "Anfrage muss ein JSON-Objekt enthalten"})
		return false
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(dst); err != nil {
		respond(w, 400, model.Error{Error: "Ungültige JSON-Anfrage: " + err.Error()})
		return false
	}
	return true
}
func jobResponse(w http.ResponseWriter, j model.Job, err error) {
	if err != nil {
		code := 400
		if errors.Is(err, ErrBusy) {
			code = 409
		}
		if errors.Is(err, ErrClosed) {
			code = 503
		}
		respond(w, code, model.Error{Error: err.Error()})
		return
	}
	respond(w, 202, j)
}
func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
