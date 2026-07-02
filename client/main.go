package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"chatpoc/protocol"
)

const sessionFile = ".chat_session"

func main() {
	addr := flag.String("addr", "localhost:8000", "Server address to connect to")
	flag.Parse()

	sessionID := loadOrCreateSession()

	// Prompt for username.
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter username: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)
	if username == "" {
		log.Fatal("username cannot be empty")
	}

	// Connect to the server (or load balancer).
	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		log.Fatalf("failed to connect to %s: %v", *addr, err)
	}
	defer conn.Close()
	fmt.Printf("Connected to %s\n", *addr)

	// Send JOIN.
	joinMsg := protocol.Message{
		Type:    protocol.MsgJoin,
		User:    username,
		Session: sessionID,
	}
	if err := protocol.WriteMessage(conn, joinMsg); err != nil {
		log.Fatalf("failed to send join: %v", err)
	}

	// Receive loop in a background goroutine.
	go func() {
		for {
			msg, err := protocol.ReadMessage(conn)
			if err != nil {
				fmt.Println("\n[disconnected from server]")
				os.Exit(0)
			}
			switch msg.Type {
			case protocol.MsgUserList:
				fmt.Printf("[online] %s\n", msg.Body)
			default:
				fmt.Printf("[%s] %s\n", msg.User, msg.Body)
			}
		}
	}()

	// Main loop: read lines from stdin and send as MsgChat.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "" {
			continue
		}
		chatMsg := protocol.Message{
			Type: protocol.MsgChat,
			User: username,
			Body: text,
		}
		if err := protocol.WriteMessage(conn, chatMsg); err != nil {
			log.Printf("send error: %v", err)
			return
		}
	}
}

// loadOrCreateSession reads or creates a persistent session ID file.
func loadOrCreateSession() string {
	data, err := os.ReadFile(sessionFile)
	if err == nil {
		sid := strings.TrimSpace(string(data))
		if sid != "" {
			return sid
		}
	}

	// Generate 16 random bytes → 32-char hex string.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("failed to generate session id: %v", err)
	}
	sid := hex.EncodeToString(b)

	if err := os.WriteFile(sessionFile, []byte(sid), 0644); err != nil {
		log.Fatalf("failed to write session file: %v", err)
	}
	return sid
}
