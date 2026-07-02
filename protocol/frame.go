package protocol

import (
	"encoding/binary"
	"encoding/json"
	"io"
)

// WriteMessage serialises msg as JSON and writes it to w using the
// wire format: [4-byte big-endian uint32 length][JSON payload].
func WriteMessage(w io.Writer, msg Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	length := uint32(len(payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}

	_, err = w.Write(payload)
	return err
}

// ReadMessage reads one length-prefixed JSON frame from r and returns
// the decoded Message. It returns io.EOF on a clean disconnect.
func ReadMessage(r io.Reader) (Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return Message{}, err
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Message{}, err
	}

	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		return Message{}, err
	}

	return msg, nil
}
