package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

//go:embed dashboard.html
var dashboardHTML []byte

var (
	backends = []*Backend{
		newBackend("localhost:9001"),
		newBackend("localhost:9002"),
		newBackend("localhost:9003"),
	}

	strategyMu      sync.RWMutex
	currentStrategy Strategy = &RoundRobin{}
	strategyName             = "round-robin"
)

// Event represents a system event logged for the /events feed.
type Event struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // "up", "down", "info"
	Message string    `json:"message"`
}

var (
	eventLogMu   sync.Mutex
	eventLog     []Event
	eventIDCounter int64
)

const maxEventLog = 200

func appendEvent(evtType, message string) {
	eventLogMu.Lock()
	defer eventLogMu.Unlock()
	eventIDCounter++
	eventLog = append(eventLog, Event{
		ID:      eventIDCounter,
		Time:    time.Now(),
		Type:    evtType,
		Message: message,
	})
	if len(eventLog) > maxEventLog {
		eventLog = eventLog[len(eventLog)-maxEventLog:]
	}
}

func newBackend(addr string) *Backend {
	b := &Backend{Addr: addr}
	b.Alive.Store(true)
	return b
}

func main() {
	// Start health checker.
	go healthCheckLoop()

	// Start stats/admin HTTP server on :9000.
	go startHTTPAdmin()

	// Listen for client connections on :8000.
	ln, err := net.Listen("tcp", ":8000")
	if err != nil {
		log.Fatalf("loadbalancer: failed to listen on :8000: %v", err)
	}
	defer ln.Close()

	log.Println("loadbalancer: proxy listening on :8000")
	log.Println("loadbalancer: stats/admin HTTP on :9000")
	log.Printf("loadbalancer: backends configured: %s, %s, %s",
		backends[0].Addr, backends[1].Addr, backends[2].Addr)

	for {
		clientConn, err := ln.Accept()
		if err != nil {
			log.Printf("loadbalancer: accept error: %v", err)
			continue
		}
		go handleClient(clientConn)
	}
}

func handleClient(clientConn net.Conn) {
	// Pick a backend.
	strategyMu.RLock()
	s := currentStrategy
	strategyMu.RUnlock()

	b := s.Pick(backends)
	if b == nil {
		log.Printf("loadbalancer: no alive backends, rejecting client %s", clientConn.RemoteAddr())
		clientConn.Close()
		return
	}

	// Dial the backend.
	backendConn, err := net.DialTimeout("tcp", b.Addr, 2*time.Second)
	if err != nil {
		log.Printf("loadbalancer: failed to dial backend %s: %v", b.Addr, err)
		clientConn.Close()
		return
	}

	b.ActiveConns.Add(1)
	log.Printf("loadbalancer: routed %s -> %s (active=%d)",
		clientConn.RemoteAddr(), b.Addr, b.ActiveConns.Load())

	defer func() {
		b.ActiveConns.Add(-1)
		clientConn.Close()
		backendConn.Close()
		log.Printf("loadbalancer: closed %s -> %s (active=%d)",
			clientConn.RemoteAddr(), b.Addr, b.ActiveConns.Load())
	}()

	// Pipe bytes bidirectionally.
	done := make(chan struct{}, 1)

	go func() {
		io.Copy(backendConn, clientConn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(clientConn, backendConn)
		done <- struct{}{}
	}()

	// Wait for either direction to finish.
	<-done
}

// healthCheckLoop probes each backend every 2 seconds and updates Alive status.
func healthCheckLoop() {
	for {
		time.Sleep(2 * time.Second)
		for _, b := range backends {
			conn, err := net.DialTimeout("tcp", b.Addr, 1*time.Second)
			wasAlive := b.Alive.Load()
			if err != nil {
				if wasAlive {
					b.Alive.Store(false)
					log.Printf("loadbalancer: backend %s is DOWN", b.Addr)
					appendEvent("down", fmt.Sprintf("%s stopped responding", b.Addr))
				}
			} else {
				conn.Close()
				if !wasAlive {
					b.Alive.Store(true)
					log.Printf("loadbalancer: backend %s is UP", b.Addr)
					appendEvent("up", fmt.Sprintf("%s recovered", b.Addr))
				}
			}
		}
	}
}

// --- HTTP admin/stats endpoints ---

type backendStats struct {
	Addr        string `json:"addr"`
	Alive       bool   `json:"alive"`
	ActiveConns int64  `json:"activeConns"`
}

type statsResponse struct {
	Backends []backendStats `json:"backends"`
	Strategy string         `json:"strategy"`
}

func startHTTPAdmin() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/stats", handleStats)
	mux.HandleFunc("/strategy", handleStrategy)
	mux.HandleFunc("/events", handleEvents)

	log.Println("loadbalancer: starting HTTP admin on :9000")
	if err := http.ListenAndServe(":9000", mux); err != nil {
		log.Fatalf("loadbalancer: HTTP admin error: %v", err)
	}
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := make([]backendStats, len(backends))
	for i, b := range backends {
		stats[i] = backendStats{
			Addr:        b.Addr,
			Alive:       b.Alive.Load(),
			ActiveConns: b.ActiveConns.Load(),
		}
	}

	strategyMu.RLock()
	name := strategyName
	strategyMu.RUnlock()

	resp := statsResponse{Backends: stats, Strategy: name}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleStrategy(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	strategyMu.Lock()
	switch name {
	case "round-robin":
		currentStrategy = &RoundRobin{}
		strategyName = "round-robin"
	case "least-connections":
		currentStrategy = &LeastConnections{}
		strategyName = "least-connections"
	default:
		strategyMu.Unlock()
		http.Error(w, fmt.Sprintf("invalid strategy: %q (valid: round-robin, least-connections)", name), http.StatusBadRequest)
		return
	}
	strategyMu.Unlock()

	appendEvent("info", fmt.Sprintf("strategy switched to %s", name))
	log.Printf("loadbalancer: strategy changed to %s", name)
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "strategy set to %s\n", name)
}

// --- Events feed ---

type eventsResponse struct {
	Events []Event `json:"events"`
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	eventLogMu.Lock()
	evts := make([]Event, len(eventLog))
	copy(evts, eventLog)
	eventLogMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(eventsResponse{Events: evts})
}
