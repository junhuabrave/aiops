package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var taskID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func taskResponse(w http.ResponseWriter, v any, err error, status int) {
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "Investigation not found", 404)
		case errors.Is(err, errQueueFull):
			w.Header().Set("Retry-After", "5")
			http.Error(w, err.Error(), 429)
		case errors.Is(err, errIDConflict):
			http.Error(w, err.Error(), 409)
		default:
			log.Printf("task API: %v", err)
			http.Error(w, "Investigation storage unavailable; retry with the same request ID", 503)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// All endpoints inherit the console's session authorization. The demo has one
// broad operator scope; these are not multi-tenant or independently scoped APIs.
func taskRoutes(mux *http.ServeMux, s *taskStore, run string, authorized func(*http.Request) bool) {
	mux.HandleFunc("POST /api/investigations", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			Chain    string `json:"chain"`
		}
		if decodeJSON(w, r, &input, 4096) != nil || !taskID.MatchString(input.ID) ||
			len(input.Question) > 500 || strings.TrimSpace(input.Question) == "" || (input.Chain != "a" && input.Chain != "b") {
			http.Error(w, "Supply a 32-character lowercase hex request ID, chain a or b and a question of 1–500 bytes", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		j, err := s.submit(ctx, input.ID, run, input.Chain, input.Question)
		taskResponse(w, j, err, 202)
	})
	mux.HandleFunc("GET /api/investigations", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		jobs, err := s.list(ctx)
		taskResponse(w, jobs, err, 200)
	})
	mux.HandleFunc("GET /api/investigations/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		j, err := s.get(ctx, r.PathValue("id"))
		taskResponse(w, j, err, 200)
	})
	mux.HandleFunc("POST /api/investigations/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		j, err := s.cancel(ctx, r.PathValue("id"))
		taskResponse(w, j, err, 200)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		cursor := r.Header.Get("Last-Event-ID")
		if cursor == "" {
			cursor = r.URL.Query().Get("after")
		}
		var after int64
		if cursor != "" {
			var err error
			after, err = strconv.ParseInt(cursor, 10, 64)
			if err != nil || after < 0 {
				http.Error(w, "Invalid event cursor", 400)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		f, ok := w.(http.Flusher)
		if !ok {
			return
		}
		// Finite streams bound slow connections below the HTTP write timeout.
		// EventSource reconnects with Last-Event-ID; closing it is not cancellation.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			// Logout/session expiry also stops an already-open stream.
			if !authorized(r) {
				return
			}
			events, err := s.events(ctx, after)
			if err != nil {
				return
			}
			for _, event := range events {
				b, _ := json.Marshal(event)
				if _, err = fmt.Fprintf(w, "id: %d\nevent: progress\ndata: %s\n\n", event.ID, b); err != nil {
					return
				}
				after = event.ID
			}
			fmt.Fprint(w, ": connected\n\n")
			f.Flush()
			if len(events) == 100 {
				continue // Replay the remaining durable backlog before waiting.
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
}
