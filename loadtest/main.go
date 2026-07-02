package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"chatpoc/protocol"
)

var phrases = []string{
	"hello everyone!",
	"how's it going?",
	"nice weather today",
	"anyone here?",
	"just testing",
	"Go is great",
	"load testing in progress",
	"beep boop",
	"ping!",
	"chat is working",
}

func main() {
	addr := flag.String("addr", "localhost:8000", "Server address to connect to")
	n := flag.Int("n", 20, "Number of simulated clients")
	interval := flag.Int("interval", 3, "Seconds between messages per client")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("loadtest: shutting down...")
		cancel()
	}()

	var totalSent atomic.Int64
	var wg sync.WaitGroup

	for i := 1; i <= *n; i++ {
		wg.Add(1)
		go func(botID int) {
			defer wg.Done()
			runBot(ctx, *addr, botID, time.Duration(*interval)*time.Second, &totalSent)
		}(i)
	}

	// Print periodic summary.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Printf("loadtest: %d bots, %d messages sent so far", *n, totalSent.Load())
			}
		}
	}()

	log.Printf("loadtest: started %d bots targeting %s (interval=%ds)", *n, *addr, *interval)
	wg.Wait()
	log.Printf("loadtest: finished, total messages sent: %d", totalSent.Load())
}

func runBot(ctx context.Context, addr string, botID int, interval time.Duration, totalSent *atomic.Int64) {
	username := fmt.Sprintf("bot%d", botID)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Printf("loadtest: %s failed to connect: %v", username, err)
		return
	}
	defer conn.Close()

	// Generate a random session ID.
	sessionBytes := make([]byte, 16)
	if _, err := rand.Read(sessionBytes); err != nil {
		log.Printf("loadtest: %s session gen error: %v", username, err)
		return
	}
	sessionID := hex.EncodeToString(sessionBytes)

	// Send JOIN.
	joinMsg := protocol.Message{
		Type:    protocol.MsgJoin,
		User:    username,
		Session: sessionID,
	}
	if err := protocol.WriteMessage(conn, joinMsg); err != nil {
		log.Printf("loadtest: %s join error: %v", username, err)
		return
	}

	// Reader goroutine: drain incoming messages silently.
	go func() {
		for {
			_, err := protocol.ReadMessage(conn)
			if err != nil {
				return
			}
		}
	}()

	// Send messages at the configured interval.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Send LEAVE on graceful shutdown.
			leaveMsg := protocol.Message{Type: protocol.MsgLeave, User: username}
			protocol.WriteMessage(conn, leaveMsg)
			return
		case <-ticker.C:
			phrase := pickRandom(phrases)
			chatMsg := protocol.Message{
				Type: protocol.MsgChat,
				User: username,
				Body: phrase,
			}
			if err := protocol.WriteMessage(conn, chatMsg); err != nil {
				log.Printf("loadtest: %s send error: %v", username, err)
				return
			}
			totalSent.Add(1)
		}
	}
}

func pickRandom(items []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(items))))
	if err != nil {
		return items[0]
	}
	return items[n.Int64()]
}
