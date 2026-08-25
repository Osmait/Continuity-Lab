package gossip

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const MaxDatagram = 4096

type Message struct {
	Version      int    `json:"version"`
	RepoID       string `json:"repo_id"`
	Sequence     uint64 `json:"sequence"`
	HeadRevision uint64 `json:"head_revision"`
	HeadETag     string `json:"head_etag"`
	Sender       string `json:"sender"`
	SentAtUnixMS int64  `json:"sent_at_unix_ms"`
	Nonce        string `json:"nonce"`
	HMAC         string `json:"hmac"`
}

func Sign(message Message, secret []byte) ([]byte, error) {
	message.Version = 1
	message.HMAC = ""
	unsigned, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(unsigned)
	message.HMAC = hex.EncodeToString(mac.Sum(nil))
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxDatagram {
		return nil, errors.New("gossip datagram exceeds 4 KiB")
	}
	return encoded, nil
}

func Verify(data, secret []byte, now time.Time) (Message, error) {
	if len(data) == 0 || len(data) > MaxDatagram {
		return Message{}, errors.New("invalid gossip datagram size")
	}
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Message{}, err
	}
	provided, err := hex.DecodeString(message.HMAC)
	if err != nil {
		return Message{}, errors.New("invalid gossip HMAC encoding")
	}
	message.HMAC = ""
	unsigned, err := json.Marshal(message)
	if err != nil {
		return Message{}, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(unsigned)
	if message.Version != 1 || !hmac.Equal(provided, mac.Sum(nil)) {
		return Message{}, errors.New("invalid gossip signature")
	}
	if delta := now.UnixMilli() - message.SentAtUnixMS; delta > int64((5*time.Minute)/time.Millisecond) || delta < -int64((time.Minute)/time.Millisecond) {
		return Message{}, errors.New("stale gossip message")
	}
	message.HMAC = hex.EncodeToString(provided)
	return message, nil
}
