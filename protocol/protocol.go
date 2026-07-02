package protocol

// MessageType identifies the kind of message being sent over the wire.
type MessageType string

const (
	MsgJoin     MessageType = "JOIN"
	MsgChat     MessageType = "CHAT"
	MsgUserList MessageType = "USERLIST"
	MsgLeave    MessageType = "LEAVE"
	MsgPing     MessageType = "PING"
	MsgPong     MessageType = "PONG"
)

// Message is the single envelope type for all communication between
// client and server. It is serialised as JSON inside a length-prefixed frame.
type Message struct {
	Type    MessageType `json:"type"`
	User    string      `json:"user,omitempty"`
	Body    string      `json:"body,omitempty"`
	Session string      `json:"session,omitempty"`
}
