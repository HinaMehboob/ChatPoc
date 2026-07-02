package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"chatpoc/protocol"
)

var (
	mu       sync.Mutex
	backends = make(map[net.Conn]bool)
)

// LoggedMessage is a record of a relayed CHAT message, exposed via /messages.
type LoggedMessage struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
	User string    `json:"user"`
	Body string    `json:"body"`
}

var (
	msgLogMu  sync.Mutex
	messageLog []LoggedMessage
	msgIDCounter int64
)

const maxMessageLog = 200

// appendMessage adds a chat message to the log (capped at maxMessageLog).
func appendMessage(user, body string) {
	msgLogMu.Lock()
	defer msgLogMu.Unlock()
	msgIDCounter++
	entry := LoggedMessage{
		ID:   msgIDCounter,
		Time: time.Now(),
		User: user,
		Body: body,
	}
	messageLog = append(messageLog, entry)
	if len(messageLog) > maxMessageLog {
		messageLog = messageLog[len(messageLog)-maxMessageLog:]
	}
}

func main() {
	// Start HTTP API on :7001.
	go startHTTPAPI()

	ln, err := net.Listen("tcp", ":7000")
	if err != nil {
		log.Fatalf("relay: failed to listen on :7000: %v", err)
	}
	defer ln.Close()
	log.Println("relay: TCP listening on :7000")
	log.Println("relay: HTTP API listening on :7001")

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("relay: accept error: %v", err)
			continue
		}

		mu.Lock()
		backends[conn] = true
		mu.Unlock()
		log.Printf("relay: backend connected (%s), total=%d", conn.RemoteAddr(), len(backends))

		go handleBackend(conn)
	}
}

func handleBackend(conn net.Conn) {
	defer func() {
		mu.Lock()
		delete(backends, conn)
		remaining := len(backends)
		mu.Unlock()
		conn.Close()
		log.Printf("relay: backend disconnected (%s), total=%d", conn.RemoteAddr(), remaining)
	}()

	var msgCount int64
	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			if msgCount > 0 {
				log.Printf("relay: backend %s disconnected after %d messages: %v", conn.RemoteAddr(), msgCount, err)
			}
			return
		}
		msgCount++

		// Log CHAT messages for the HTTP feed.
		if msg.Type == protocol.MsgChat {
			appendMessage(msg.User, msg.Body)
		}

		// Fan out to every OTHER connected backend.
		mu.Lock()
		targets := make([]net.Conn, 0, len(backends)-1)
		for c := range backends {
			if c != conn {
				targets = append(targets, c)
			}
		}
		mu.Unlock()

		for _, c := range targets {
			if err := protocol.WriteMessage(c, msg); err != nil {
				log.Printf("relay: write error to %s: %v", c.RemoteAddr(), err)
			}
		}
	}
}

// --- HTTP API ---

type messagesResponse struct {
	Messages []LoggedMessage `json:"messages"`
}

func startHTTPAPI() {
	mux := http.NewServeMux()
	mux.HandleFunc("/messages", handleMessages)

	if err := http.ListenAndServe(":7001", mux); err != nil {
		log.Fatalf("relay: HTTP API error: %v", err)
	}
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	msgLogMu.Lock()
	// Copy the slice so we don't hold the lock during JSON encoding.
	msgs := make([]LoggedMessage, len(messageLog))
	copy(msgs, messageLog)
	msgLogMu.Unlock()

	resp := messagesResponse{Messages: msgs}
	json.NewEncoder(w).Encode(resp)
}
