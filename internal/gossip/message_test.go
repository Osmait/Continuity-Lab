package gossip

import (
	"testing"
	"time"
)

func TestSignVerifyAndTamper(t *testing.T) {
	now := time.Unix(1000, 0)
	secret := []byte("shared-secret-long-enough")
	encoded, err := Sign(Message{RepoID: "repo", Sequence: 2, Sender: "node-a", SentAtUnixMS: now.UnixMilli(), Nonce: "n"}, secret)
	if err != nil {
		t.Fatal(err)
	}
	message, err := Verify(encoded, secret, now)
	if err != nil || message.Sequence != 2 {
		t.Fatalf("verify: %#v %v", message, err)
	}
	encoded[len(encoded)-2] ^= 1
	if _, err := Verify(encoded, secret, now); err == nil {
		t.Fatal("tampered message accepted")
	}
}
