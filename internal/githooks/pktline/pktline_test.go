package pktline

import (
	"bytes"
	"errors"
	"testing"
)

func TestGoldenPackets(t *testing.T) {
	var output bytes.Buffer
	if err := Write(&output, []byte("version=1\x00atomic\n")); err != nil {
		t.Fatal(err)
	}
	if err := Flush(&output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "0015version=1\x00atomic\n0000"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	reader := NewReader(&output)
	payload, err := reader.Read()
	if err != nil || string(payload) != "version=1\x00atomic\n" {
		t.Fatalf("read %q: %v", payload, err)
	}
	if _, err := reader.Read(); !errors.Is(err, ErrFlush) {
		t.Fatalf("flush error = %v", err)
	}
}

func TestInvalidAndTruncatedPackets(t *testing.T) {
	for _, input := range []string{"zzzz", "0003", "0008ab", "ffff"} {
		if _, err := NewReader(bytes.NewBufferString(input)).Read(); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}
