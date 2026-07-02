# ChatPOC — TCP Chat with Load Balancing & Cross-Backend Relay

A proof-of-concept TCP chat application in Go featuring a multi-backend architecture with a message relay for cross-server communication, a TCP load balancer with pluggable strategies, a live metrics dashboard, and a load-testing tool.

## Architecture

```
                          ┌──────────────┐
                          │    Relay     │
                          │  (TCP :7000) │
                          └──┬───┬───┬──┘
                  ┌──────────┘   │   └──────────┐
                  │              │              │
             ┌────▼───┐    ┌────▼───┐    ┌────▼───┐
             │Backend │    │Backend │    │Backend │
             │ :9001  │    │ :9002  │    │ :9003  │
             └────▲───┘    └────▲───┘    └────▲───┘
                  │              │              │
                  └──────────┐   │   ┌──────────┘
                          ┌──▼───▼───▼──┐
                          │Load Balancer│
                          │ TCP  :8000  │
                          │ HTTP :9000  │
                          └──────▲──────┘
                       ┌─────────┼─────────┐
                  ┌────┴───┐ ┌───┴────┐ ┌──┴─────┐
                  │Client A│ │Client B│ │Client C│
                  └────────┘ └────────┘ └────────┘
```

**Data flow:** Clients connect to the Load Balancer (:8000), which routes them to a backend server. Each backend forwards chat messages to the Relay (:7000), which fans them out to all other backends. This ensures that clients on different backends can chat with each other.

## Prerequisites

- **Go 1.21+** (tested with Go 1.26)

## Build

```bash
cd chatpoc
go build ./...
```

This compiles all packages: `protocol`, `server`, `client`, `relay`, `loadbalancer`, and `loadtest`.

## How to Run

Start each component in a **separate terminal**, in the order shown below. All commands assume you are in the `chatpoc` directory.

### 1. Start the Relay (port 7000)

```powershell
go run ./relay
```

### 2. Start Three Backend Servers (ports 9001, 9002, 9003)

```powershell
# Terminal 2
go run ./server -port 9001 -relay localhost:7000
```

```powershell
# Terminal 3
go run ./server -port 9002 -relay localhost:7000
```

```powershell
# Terminal 4
go run ./server -port 9003 -relay localhost:7000
```

### 3. Start the Load Balancer (proxy :8000, dashboard :9000)

```powershell
go run ./loadbalancer
```

### 4. Open the Dashboard

Open your browser to: **http://localhost:9000**

You will see a live dashboard showing all three backends with green/red status dots, active connection counts, and the current load-balancing strategy.

### 5. Start Interactive Chat Clients

```powershell
# Terminal 6
go run ./client -addr localhost:8000
# Enter username: alice
```

```powershell
# Terminal 7
go run ./client -addr localhost:8000
# Enter username: bob
```

Alice and Bob will be routed to different backends (check the load balancer logs). They can chat with each other because the relay fans messages between backends.

### 6. Run the Load Test

```powershell
go run ./loadtest -n 30
```

This spawns 30 simulated bot clients that send random messages every 3 seconds. Watch the dashboard to see connection counts rise across backends. Press `Ctrl+C` to stop.

## Demo: Failover

1. With everything running, **kill one backend server** by pressing `Ctrl+C` in its terminal (e.g., the one on port 9002).
2. Watch the dashboard — within ~2 seconds, that backend's status dot turns **red**.
3. New client connections are automatically routed only to the remaining alive backends.
4. Clients already connected to the killed backend will be disconnected, but clients on other backends are **unaffected**.

## Demo: Strategy Switching

**Via the dashboard:** Click the "Round Robin" or "Least Connections" button.

**Via curl:**

```bash
# Switch to least-connections
curl -X POST "http://localhost:9000/strategy?name=least-connections"

# Switch back to round-robin
curl -X POST "http://localhost:9000/strategy?name=round-robin"

# Check current stats
curl http://localhost:9000/stats
```

## Known Limitations

- Clients connected to a backend that dies are **dropped, not migrated** to a healthy backend.
- **No TLS/encryption** — all traffic is plaintext TCP.
- **No persistent message history** — messages are not stored anywhere.
- **Single global chat room** only — no rooms or channels.
- Reconnection recognizes a returning session but **does not replay messages** missed while offline.
- The load balancer operates at the TCP layer (byte-level proxy) and is not aware of the chat protocol.

## Stretch Goals (Not Implemented)

- Chat rooms / channels
- Offline message queuing and replay on reconnect
- A third load-balancing strategy (e.g., weighted round-robin, random)
- TLS encryption for all connections
- Docker Compose one-command startup
- Persistent storage (database-backed message history)
