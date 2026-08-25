package procreceive

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/continuity-lab/continuity-lab/internal/githooks/pktline"
	"github.com/continuity-lab/continuity-lab/internal/model"
)

func TestVersionAndCommandGoldenStream(t *testing.T) {
	version, features, err := parseVersion([]byte("version=1\x00push-options atomic\n"))
	if err != nil || version != "version=1" || !features["push-options"] || !features["atomic"] {
		t.Fatalf("version parse: %q %#v %v", version, features, err)
	}
	update := model.RefUpdate{OldOID: model.ZeroOID, NewOID: "1111111111111111111111111111111111111111", Ref: "refs/heads/main"}
	var stream bytes.Buffer
	_ = pktline.Write(&stream, []byte(update.OldOID+" "+update.NewOID+" "+update.Ref+"\n"))
	_ = pktline.Flush(&stream)
	got, err := readCommands(pktline.NewReader(&stream), 10)
	if err != nil || !reflect.DeepEqual(got, []model.RefUpdate{update}) {
		t.Fatalf("commands=%#v err=%v", got, err)
	}
}

func TestCommandFailuresAndResultGolden(t *testing.T) {
	for _, payload := range []string{"bad\n", model.ZeroOID + " 1111111111111111111111111111111111111111 invalid\n"} {
		var stream bytes.Buffer
		_ = pktline.Write(&stream, []byte(payload))
		_ = pktline.Flush(&stream)
		if _, err := readCommands(pktline.NewReader(&stream), 1); err == nil {
			t.Fatalf("accepted %q", payload)
		}
	}
	commands := []model.RefUpdate{{Ref: "refs/heads/main"}}
	var ok bytes.Buffer
	if err := report(&ok, commands, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := ok.String(), "0017ok refs/heads/main\n0000"; got != want {
		t.Fatalf("ok=%q want %q", got, want)
	}
	var ng bytes.Buffer
	if err := report(&ng, commands, errors.New("no\nway")); err != nil {
		t.Fatal(err)
	}
	if got, want := ng.String(), "001eng refs/heads/main no way\n0000"; got != want {
		t.Fatalf("ng=%q want %q", got, want)
	}
}
