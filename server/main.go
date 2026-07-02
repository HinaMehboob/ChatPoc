package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"chatpoc/protocol"
)

// Client represents a connected chat user.
type Client struct {
	conn net.Conn
	user string
}

var (
	mu       sync.Mutex
	clients  = make(map[string]*Client) // username -> *Client
	sessions = make(map[string]string)  // session ID -> username

	relayMu   sync.Mutex
	relayConn net.Conn // nil if not connected
)

func main() {
	port := flag.String("port", "9001", "TCP port to listen on")
	relayAddr := flag.String("relay", "localhost:7000", "Relay server address")
	flag.Parse()

	// Connect to relay (optional — don't crash if unavailable).
	rc, err := net.Dial("tcp", *relayAddr)
	if err != nil {
		log.Printf("WARNING: relay not available at %s: %v (running without relay)", *relayAddr, err)
	} else {
		relayConn = rc
		log.Printf("connected to relay at %s", *relayAddr)
		go relayReadLoop(rc)
	}

	ln, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatalf("failed to listen on port %s: %v", *port, err)
	}
	defer ln.Close()
	log.Printf("chat server listening on port %s", *port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go handleConn(conn)
	}
}

// relayReadLoop reads messages from the relay and broadcasts them to all local clients.
func relayReadLoop(conn net.Conn) {
	defer func() {
		relayMu.Lock()
		relayConn = nil
		relayMu.Unlock()
		conn.Close()
		log.Println("relay connection closed")
	}()

	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			log.Printf("relay read error: %v", err)
			return
		}
		// Broadcast to all local clients (no exclusions — this message
		// came from another backend's user, not a local one).
		broadcastLocal(msg, "")
	}
}

// forwardToRelay sends a message to the relay if connected.
func forwardToRelay(msg protocol.Message) {
	relayMu.Lock()
	rc := relayConn
	relayMu.Unlock()

	if rc == nil {
		return
	}
	if err := protocol.WriteMessage(rc, msg); err != nil {
		log.Printf("relay write error: %v", err)
	}
}

func handleConn(conn net.Conn) {
	var username string
	defer func() {
		conn.Close()
		if username != "" {
			removeClient(username)
		}
	}()

	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			if username != "" {
				log.Printf("read error for %s: %v", username, err)
			}
			return
		}

		switch msg.Type {
		case protocol.MsgJoin:
			username = msg.User
			handleJoin(conn, username, msg.Session)

		case protocol.MsgChat:
			// Broadcast locally to other clients.
			broadcastLocal(msg, username)
			// Forward to relay so other backends receive it.
			forwardToRelay(msg)

		case protocol.MsgLeave:
			return // deferred cleanup handles remove + close

		case protocol.MsgPing:
			pong := protocol.Message{Type: protocol.MsgPong}
			if err := protocol.WriteMessage(conn, pong); err != nil {
				log.Printf("pong write error for %s: %v", username, err)
			}
		}
	}
}

// handleJoin registers the client, checks for session reconnection,
// broadcasts a join notification, and sends the user list to the joiner.
func handleJoin(conn net.Conn, username, sessionID string) {
	mu.Lock()

	// Check for reconnection via session ID.
	if sessionID != "" {
		if prev, ok := sessions[sessionID]; ok && prev == username {
			log.Printf("user %s reconnected (session %s)", username, sessionID)
		} else {
			sessions[sessionID] = username
		}
	}

	clients[username] = &Client{conn: conn, user: username}

	// Build the user list while holding the lock.
	names := make([]string, 0, len(clients))
	for name := range clients {
		names = append(names, name)
	}

	mu.Unlock()

	log.Printf("user %s joined", username)

	// Broadcast join notification to everyone except the new client.
	joinMsg := protocol.Message{
		Type: protocol.MsgChat,
		User: "server",
		Body: fmt.Sprintf("%s joined", username),
	}
	broadcastLocal(joinMsg, username)

	// Send the user list to the joining client.
	userList := protocol.Message{
		Type: protocol.MsgUserList,
		Body: strings.Join(names, ","),
	}
	if err := protocol.WriteMessage(conn, userList); err != nil {
		log.Printf("failed to send user list to %s: %v", username, err)
	}
}

// broadcastLocal sends msg to every connected LOCAL client except the one
// with the given excludeUser name. It copies the target list under the mutex,
// then writes outside the lock so slow clients don't block everyone.
func broadcastLocal(msg protocol.Message, excludeUser string) {
	mu.Lock()
	targets := make([]*Client, 0, len(clients))
	for _, c := range clients {
		if c.user != excludeUser {
			targets = append(targets, c)
		}
	}
	mu.Unlock()

	for _, c := range targets {
		if err := protocol.WriteMessage(c.conn, msg); err != nil {
			log.Printf("write error to %s: %v", c.user, err)
		}
	}
}

// removeClient deletes the user from the client map, closes nothing
// (caller handles close), and broadcasts a leave notification.
func removeClient(username string) {
	mu.Lock()
	delete(clients, username)
	mu.Unlock()

	log.Printf("user %s left", username)

	leaveMsg := protocol.Message{
		Type: protocol.MsgChat,
		User: "server",
		Body: fmt.Sprintf("%s left", username),
	}
	broadcastLocal(leaveMsg, "")
}
