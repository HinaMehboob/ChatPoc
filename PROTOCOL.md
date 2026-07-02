# Chat Protocol Specification

## Wire Frame Format

Every message on the wire is a **length-prefixed JSON frame**:

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                    Payload Length (uint32)                     |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
|                    UTF-8 JSON Payload                          |
|                    (variable length)                           |
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

- **Bytes 0–3**: Payload length as a 4-byte **big-endian unsigned 32-bit integer**.
- **Bytes 4–(4+length-1)**: The JSON-encoded `Message` struct, UTF-8 encoded.

### Concrete Example

For the JSON payload `{"type":"CHAT","user":"alice","body":"hi"}` (42 bytes):

```
Bytes on the wire:
  00 00 00 2A  7B 22 74 79 70 65 22 3A 22 43 48 41 ...
  ├──────────┤ ├──────────────────────────────────── ...
  length = 42   JSON payload (42 bytes)
```

## Message JSON Schema

| Field     | Type   | JSON Tag             | Description                          |
|-----------|--------|----------------------|--------------------------------------|
| `type`    | string | `"type"`             | One of the six message types below.  |
| `user`    | string | `"user,omitempty"`   | Username of the sender.              |
| `body`    | string | `"body,omitempty"`   | Message body / payload data.         |
| `session` | string | `"session,omitempty"`| Session ID for reconnection.         |

## Message Types

| Type       | Direction        | Purpose                                                            |
|------------|------------------|--------------------------------------------------------------------|
| `JOIN`     | Client → Server  | Client announces its username and session ID to join the chat.     |
| `CHAT`     | Bidirectional    | A chat message. Server also uses this for system announcements.    |
| `USERLIST` | Server → Client  | Sent to a newly joined client; `body` is a comma-separated list of online usernames. |
| `LEAVE`    | Client → Server  | Client announces a graceful disconnect.                            |
| `PING`     | Client → Server  | Client health-check request.                                      |
| `PONG`     | Server → Client  | Server reply to a `PING`.                                         |

## Why Length-Prefixed Framing?

TCP is a byte-stream protocol — it provides no inherent message boundaries. A common alternative is delimiter-based framing (e.g. newline-terminated), but this introduces ambiguity if the payload itself contains the delimiter character. With JSON payloads that may include arbitrary user-typed text, a newline or any other sentinel character could legitimately appear inside the data.

Length-prefixed framing avoids this entirely: the receiver first reads the fixed 4-byte header to learn exactly how many bytes constitute the next message, then reads precisely that many bytes. There is no ambiguity, no need to escape delimiters, and the parser can allocate the exact buffer size up front. This approach is simple, efficient, and unambiguous for any payload content.