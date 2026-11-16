package main

import (
	"context"
	"net/http"
	"time"
)

// Inherits the console's operator session and CSRF checks. A worker cannot reach
// this listener. Profiles are operator-selected evidence scopes, not viewer roles.
func sandboxRoutes(mux *http.ServeMux, s *taskStore, run string) {
	mux.HandleFunc("POST /api/sandbox-investigations", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID      string `json:"id"`
			Source  string `json:"source_id"`
			Profile string `json:"profile"`
		}
		if decodeJSON(w, r, &input, 4096) != nil || !taskID.MatchString(input.ID) || !taskID.MatchString(input.Source) ||
			(input.Profile != "chain" && input.Profile != "subscriber-1") {
			http.Error(w, "Supply request/source IDs and profile chain or subscriber-1", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		j, err := s.submitSandbox(ctx, input.ID, input.Source, run, input.Profile)
		taskResponse(w, j, err, 202)
	})
	mux.HandleFunc("GET /api/sandbox-investigations/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		j, err := s.getSandbox(ctx, r.PathValue("id"))
		taskResponse(w, j, err, 200)
	})
	mux.HandleFunc("POST /api/sandbox-investigations/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		j, err := s.cancelSandbox(ctx, r.PathValue("id"))
		taskResponse(w, j, err, 200)
	})
}
