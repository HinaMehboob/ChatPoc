# Chat Protocol Specification

**Version:** 1.0
**Transport:** TCP (raw sockets)
**Encoding:** Length-prefixed JSON frames

---

## 1. Design Rationale

This protocol is a custom application-layer protocol built on top of raw TCP.

**Why TCP over UDP:** The requirements call for reliable, in-order, non-corrupted message delivery. TCP provides this natively (retransmission, ordering, congestion control). UDP was considered but rejected — using it would require rebuilding TCP's reliability guarantees by hand, which is unnecessary complexity for this PoC.

**Why length-prefixed framing over delimiter-based framing:** TCP delivers a byte *stream*, not discrete messages — a single `read()` call may return a partial message, multiple messages, or anything in between. Length-prefixing solves this cleanly: the receiver always knows exactly how many bytes to wait for before it has one complete message, and there's no risk of a delimiter character accidentally appearing inside message content (which can happen with newline-delimited schemes if the body isn't escaped).

**Why JSON over pure binary encoding:** JSON payloads are human-readable and easy to debug (e.g., inspectable in Wireshark/tcpdump), while still being fast enough for this PoC's scale (a few dozen clients, text-only messages). The 4-byte length header still gives us proper binary framing at the transport boundary — JSON is only used for the payload contents.

---

## 2. Frame Format

Every message sent over the wire — in either direction, and between backend servers and the relay — uses the same frame structure:

```
+----------------------+----------------------------+
|  Length (4 bytes)    |  Payload (N bytes)         |
|  big-endian uint32   |  UTF-8 encoded JSON         |
+----------------------+----------------------------+
```

- **Length field:** a 4-byte unsigned integer, big-endian byte order, indicating the exact number of bytes in the payload that follows. Does not include the 4 header bytes themselves.
- **Payload:** a UTF-8 encoded JSON object matching the `Message` schema (Section 3).

### Example byte layout

For the payload `{"type":"CHAT","user":"alice","body":"hi"}` (43 bytes):

```
Byte offset:   0    1    2    3    4 ...................... 46
Content:     [ 0x00 0x00 0x00 0x2B ][ {"type":"CHAT","user":"alice","body":"hi"} ]
              \_______length=43_______/  \_____________ 43 bytes of JSON ____________/
```

### Reading a frame (receiver-side algorithm)

1. Read exactly 4 bytes → decode as big-endian `uint32` → this is `length`.
2. Read exactly `length` bytes → this is the JSON payload.
3. Parse the payload into a `Message` struct.

Both reads **must** loop until the full byte count is obtained (e.g. via `io.ReadFull` in Go) — a single socket read is never assumed to return a complete frame. This is how partial reads/writes are handled correctly.

### Writing a frame (sender-side algorithm)

1. Serialize the `Message` to JSON.
2. Compute its byte length, encode as a 4-byte big-endian header.
3. Write the header, then write the payload. (Most standard library `Write` calls already handle partial writes internally by blocking until all bytes are sent or an error occurs; this protocol does not require additional write-side looping beyond checking the returned error.)

---

## 3. Message Schema

```json
{
  "type": "CHAT",
  "user": "alice",
  "body": "hello everyone",
  "session": "a1b2c3d4-...."
}
```

| Field     | Type   | Required | Description                                                                 |
|-----------|--------|----------|-------------------------------------------------------------------------------|
| `type`    | string | Yes      | One of the message types in Section 4.                                       |
| `user`    | string | Depends  | Username of the sender. Required for JOIN/CHAT; empty/omitted for server-originated messages. |
| `body`    | string | No       | Free-text content. Used for CHAT messages and server notifications.          |
| `session` | string | No       | Client-generated persistent session identifier, used for reconnection (Section 6). Sent on JOIN. |

Unused fields are omitted rather than sent as empty strings, to keep frames small.

---

## 4. Message Types

| Type       | Direction         | Purpose                                                                 |
|------------|--------------------|--------------------------------------------------------------------------|
| `JOIN`     | Client → Server    | Register/authenticate with a username and (optionally) an existing session ID. |
| `CHAT`     | Bidirectional      | A chat message. Client → Server to send; Server → Client to broadcast.   |
| `USERLIST` | Server → Client    | Current list of online users, sent on join/leave or on request.          |
| `LEAVE`    | Client → Server    | Explicit graceful disconnect notification (optional; abrupt socket close is also handled). |
| `PING`     | Client → Server    | Liveness probe / keepalive.                                              |
| `PONG`     | Server → Client    | Response to PING.                                                        |

### 4.1 JOIN

Sent once, immediately after connecting.

```json
{ "type": "JOIN", "user": "alice", "session": "a1b2c3d4-...." }
```

- If `session` is omitted or unrecognized by the server, a new logical session is created.
- If `session` matches a previously seen session ID, the server treats this as a reconnecting client rather than a new user (see Section 6).

Server responds with a `USERLIST` and broadcasts a `CHAT` system notification (`user: "server"`, `body: "alice joined"`) to existing clients.

### 4.2 CHAT

```json
{ "type": "CHAT", "user": "alice", "body": "hello everyone" }
```

Client → Server: the message to broadcast.
Server → Client: rebroadcast to all other locally-connected clients, and forwarded to the relay for cross-backend delivery (see Section 7).

### 4.3 USERLIST

```json
{ "type": "USERLIST", "body": "alice,bob,carol" }
```

Comma-separated list of currently online usernames (kept as a simple string for PoC simplicity; a JSON array is an acceptable and equally valid variant).

### 4.4 LEAVE

```json
{ "type": "LEAVE", "user": "alice" }
```

Optional explicit-disconnect message. The server also treats an abrupt socket close (read error / EOF) as an implicit leave — LEAVE is not required for correct cleanup, just a cleaner signal when available.

### 4.5 PING / PONG

```json
{ "type": "PING" }
{ "type": "PONG" }
```

Used for basic liveness checking between client and server. The load balancer's backend health checks are separate from this (see `README.md` — the LB probes backend TCP reachability directly; it does not necessarily go through full PING/PONG at the chat-protocol level, though it may for stronger liveness guarantees).

---

## 5. Connection Lifecycle

```
Client                          Server
  |----- connect (TCP) --------->|
  |----- JOIN -------------------->|
  |<---- USERLIST -----------------|
  |<---- CHAT (system: "joined") --|   (broadcast to others)
  |----- CHAT --------------------->|
  |<---- CHAT (from others) -------|
  |----- LEAVE / socket close ---->|
  |                                | (server cleans up, broadcasts "left")
```

---

## 6. Reconnection Handling

- On first run, a client generates a random `session` identifier (e.g., a UUID) and persists it locally.
- On every subsequent connection attempt (including after an unexpected drop), the client sends this same `session` value in its JOIN message.
- The server maintains `session → last known state` (at minimum: username). If the session is recognized, the server re-associates the new connection with the existing logical identity rather than treating it as a brand-new user.
- To avoid losing messages during a brief reconnect window, the server may retain a small rolling buffer (e.g., last N broadcast messages) per session, replayed on successful re-join. Full offline message queuing for extended disconnects is out of scope for the core PoC and listed as a stretch goal.

---

## 7. Cross-Backend Message Relay

This protocol is also used, unmodified, for backend-to-relay communication:

- Each chat server backend opens a TCP connection to a central relay process on startup.
- When a backend broadcasts a `CHAT` message to its local clients, it also forwards the same framed message to the relay.
- The relay fans the message out to every other connected backend (excluding the sender), which then broadcasts it to its own local clients.

This is why the frame format is transport-agnostic — the same `ReadMessage`/`WriteMessage` logic is reused for client↔server and server↔relay communication, with no special-casing required.

---

## 8. Out of Scope (Not Covered by This Protocol Version)

- Encryption/TLS (frames are sent in plaintext for this PoC)
- Rooms/channels (all clients share a single global broadcast scope)
- Message acknowledgement/delivery receipts
- Compression
- Authentication beyond a plain username

These are candidate additions for a future protocol version, not v1.