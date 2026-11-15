package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var web embed.FS

type diagnosis struct {
	ID         string     `json:"evidence_id"`
	At         time.Time  `json:"at"`
	Chain      string     `json:"chain"`
	Conclusion string     `json:"conclusion"`
	Summary    string     `json:"summary"`
	Next       string     `json:"next"`
	Evidence   []nodeView `json:"evidence"`
	Checks     []string   `json:"checks"`
}

// A deliberately small, deterministic baseline. No question text, fault controls,
// harness receipts or model output participate in the conclusion.
func diagnose(v plantView, chain string) diagnosis {
	d := diagnosis{ID: randomID(8), At: v.At, Chain: chain, Conclusion: "supported", Checks: []string{"Operations: compared source, relay and subscriber observations.", "Engineering: checked telemetry freshness and receiver-side consumption backlog.", "Integration: change adapter not connected; change causality is not assessed."}}
	byID := map[string]nodeView{}
	for _, n := range v.Nodes {
		byID[n.ID] = n
		if n.Chain == chain {
			d.Evidence = append(d.Evidence, n)
		}
	}
	for _, n := range d.Evidence {
		if n.State == "missing" || n.State == "stale" || n.Rate == nil {
			d.Conclusion = "visibility_limited"
			d.Summary = "Delivery evidence is missing, stale, or lacks a second observation. No root cause is established."
			d.Next = "Restore collection or wait for a second fresh sample, then compare source and receiver progress. Do not infer process death from missing telemetry."
			return d
		}
	}
	pub := byID[chain+"-publisher"]
	relay := byID[chain+"-relay"]
	if len(d.Evidence) != 4 || relay.Delivery == nil || pub.Rate == nil {
		d.Conclusion = "visibility_limited"
		d.Summary = "Topology or observations are incomplete."
		return d
	}
	for _, q := range relay.Delivery.Queues {
		sub := byID[q.Subscriber]
		if q.OldestSeconds > 2 && sub.Delivery != nil && sub.Delivery.PendingAge > .1 && sub.Delivery.Pending >= 16 {
			d.Summary = "Receiver-side consumption backlog accompanies delayed relay delivery to " + q.Subscriber + ". A slow-consumption hypothesis is supported by both sides of the connection."
			d.Next = "Inspect this subscriber's consumption path before proposing a shared-relay action. A rate cap, application work or resource contention can produce this pattern; the synthetic collector does not establish which."
			return d
		}
	}
	delayed := 0
	for _, q := range relay.Delivery.Queues {
		if q.OldestSeconds > 2 {
			delayed++
		}
	}
	if delayed > 0 {
		d.Summary = "Relay queues contain delayed data without a matching receiver-consumption backlog. The delivery-path bottleneck is localized, but its cause is not established."
		d.Next = "Compare relay dispatch, write stalls and acknowledgement timing. Do not equate a synthetic rate limit with CPU exhaustion."
		return d
	}
	if *pub.Rate < 1 && byID[chain+"-sub-1"].Delivery.LastReceiveAge > 3 && byID[chain+"-sub-2"].Delivery.LastReceiveAge > 3 {
		if !v.Sessions[chain] {
			d.Summary = "No publication is observed during a declared quiet session. Fresh telemetry distinguishes expected silence from lost visibility."
			d.Next = "Keep visibility monitoring active; no traffic recovery action is proposed."
		} else {
			d.Summary = "Publication has stopped while process telemetry remains fresh; downstream silence is consistent with an upstream interruption."
			d.Next = "Inspect the publisher's input and session state. Fresh telemetry does not explain why publication stopped."
		}
		return d
	}
	for _, n := range d.Evidence {
		if n.State == "symptom" {
			d.Conclusion = "inconclusive"
			d.Summary = "A delivery symptom is present without enough discriminating evidence in this snapshot."
			d.Next = "Collect a bounded historical window and compare source, queue and consumption trends."
			return d
		}
	}
	d.Summary = "No silence or queue-age symptom is present in the current fresh observations. This is a point-in-time check, not a historical all-clear."
	d.Next = "Continue deterministic monitoring. Historical sequence-loss counters remain visible even after delivery recovers."
	return d
}

func serveConsole(c *collector, dir string, port int) error {
	token, err := os.ReadFile(filepath.Join(dir, "operator.token"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startup, stop := context.WithTimeout(ctx, 5*time.Second)
	tasks, err := openTasks(startup, databaseURL(labDatabaseDir(dir)))
	stop()
	if err != nil {
		return err
	}
	defer tasks.db.Close()
	for range 2 {
		go tasks.work(ctx, c)
		go tasks.sandboxWork(ctx, c.manifest.Run)
	}
	return (&http.Server{Addr: fmt.Sprintf(":%d", port), Handler: consoleHandler(c, tasks, string(token)), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}).ListenAndServe()
}

func consoleHandler(c *collector, tasks *taskStore, token string) http.Handler {
	mux := http.NewServeMux()
	var mu sync.Mutex
	sessions := map[string]time.Time{}
	loginAttempts := 0
	loginWindow := time.Now()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	authorized := func(r *http.Request) bool {
		cookie, err := r.Cookie("plant_session")
		if err != nil {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		until, ok := sessions[cookie.Value]
		return ok && time.Now().Before(until)
	}
	if tasks != nil {
		taskRoutes(mux, tasks, c.manifest.Run, authorized)
		sandboxRoutes(mux, tasks, c.manifest.Run)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		mu.Lock()
		if time.Since(loginWindow) > time.Minute {
			loginWindow = time.Now()
			loginAttempts = 0
		}
		loginAttempts++
		limited := loginAttempts > 20
		mu.Unlock()
		if limited {
			http.Error(w, "Too many attempts; wait one minute.", 429)
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		if decodeJSON(w, r, &input, 2048) != nil || subtle.ConstantTimeCompare([]byte(input.Token), []byte(token)) != 1 {
			http.Error(w, "Invalid lab token", 401)
			return
		}
		id := randomID(32)
		mu.Lock()
		for id, until := range sessions {
			if time.Now().After(until) {
				delete(sessions, id)
			}
		}
		if len(sessions) >= 100 {
			mu.Unlock()
			http.Error(w, "Session limit reached", 429)
			return
		}
		sessions[id] = time.Now().Add(12 * time.Hour)
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "plant_session", Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		write(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		if cookie, e := r.Cookie("plant_session"); e == nil {
			mu.Lock()
			delete(sessions, cookie.Value)
			mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "plant_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		write(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		write(w, c.view())
	})
	mux.HandleFunc("/api/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		client := http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://127.0.0.1:19090/api/v1/alerts")
		if err != nil {
			http.Error(w, "Monitoring unavailable", 503)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			http.Error(w, "Monitoring unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, io.LimitReader(resp.Body, 1<<20))
	})
	mux.HandleFunc("/api/investigate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		var input struct {
			Question string `json:"question"`
			Chain    string `json:"chain"`
		}
		if decodeJSON(w, r, &input, 4096) != nil || len(input.Question) > 500 || strings.TrimSpace(input.Question) == "" || (input.Chain != "a" && input.Chain != "b") {
			http.Error(w, "Choose chain a or b and a question of 1–500 characters.", 400)
			return
		}
		d := diagnose(c.view(), input.Chain)
		// Snapshot returned directly; no claim of durable incident/task lifecycle yet.
		write(w, d)
	})
	target, _ := url.Parse("http://127.0.0.1:13000")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(r *http.Request) {
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		r.Header.Del("X-WEBAUTH-USER")
	}
	mux.Handle("/grafana/", proxy)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := "index.html"
		if r.URL.Path != "/" {
			name = strings.TrimPrefix(r.URL.Path, "/")
		}
		if name != "index.html" && name != "app.js" && name != "style.css" {
			http.NotFound(w, r)
			return
		}
		b, e := web.ReadFile("web/" + name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		switch name {
		case "app.js":
			w.Header().Set("Content-Type", "text/javascript")
		case "style.css":
			w.Header().Set("Content-Type", "text/css")
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.Write(b)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/grafana/") {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'self'; form-action 'self'")
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin, err := url.Parse(r.Header.Get("Origin"))
			if err != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") {
				http.Error(w, "Same-origin request required", 403)
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/grafana/") && r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "JSON required", 415)
				return
			}
		}
		if (strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/login") || strings.HasPrefix(r.URL.Path, "/grafana/") {
			if !authorized(r) {
				http.Error(w, "Lab session required", 401)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
