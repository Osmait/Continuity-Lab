package model

import (
	"reflect"
	"testing"
)

func TestCanonicalName(t *testing.T) {
	t.Parallel()
	valid := map[string]string{"acme/demo.git": "acme/demo", "Acme/Deep.repo/demo": "Acme/Deep.repo/demo", "a_b/c-d": "a_b/c-d"}
	for input, want := range valid {
		if got, err := CanonicalName(input); err != nil || got != want {
			t.Errorf("CanonicalName(%q)=(%q,%v), want %q", input, got, err, want)
		}
	}
	invalid := []string{"", ".", "..", "acme//demo", "acme/../demo", `acme\\demo`, "acme/%2fdemo", "/absolute", "acme/demo/"}
	for _, input := range invalid {
		if got, err := CanonicalName(input); err == nil {
			t.Errorf("CanonicalName(%q)=%q, want error", input, got)
		}
	}
}

func TestRepoIDDeterministicAndCaseSensitive(t *testing.T) {
	if RepoID("acme/demo") != "51b27da18999bff38e618463cbbe31da7a73d2563be29dc718f0e94628626baf" {
		t.Fatal("repo ID fixture changed")
	}
	if RepoID("acme/demo") == RepoID("Acme/demo") {
		t.Fatal("repo ID must preserve case")
	}
}

func TestApplyUpdatesAtomic(t *testing.T) {
	refs := map[string]string{"refs/heads/main": "1111111111111111111111111111111111111111", "refs/heads/feature": "2222222222222222222222222222222222222222"}
	updates := []RefUpdate{{Ref: "refs/heads/main", OldOID: refs["refs/heads/main"], NewOID: "3333333333333333333333333333333333333333"}, {Ref: "refs/heads/new", OldOID: ZeroOID, NewOID: "4444444444444444444444444444444444444444"}}
	got, err := ApplyUpdates(refs, updates)
	if err != nil || got["refs/heads/main"] != updates[0].NewOID || got["refs/heads/new"] != updates[1].NewOID {
		t.Fatalf("ApplyUpdates: %#v, %v", got, err)
	}
	if refs["refs/heads/main"] == updates[0].NewOID {
		t.Fatal("input map mutated")
	}
	conflict := append([]RefUpdate(nil), updates...)
	conflict[0].OldOID = ZeroOID
	if _, err := ApplyUpdates(refs, conflict); err == nil {
		t.Fatal("same-ref stale update accepted")
	}
	unrelated := []RefUpdate{{Ref: "refs/heads/feature", OldOID: refs["refs/heads/feature"], NewOID: "5555555555555555555555555555555555555555"}}
	if _, err := ApplyUpdates(got, unrelated); err != nil {
		t.Fatalf("unrelated ref should remain compatible: %v", err)
	}
}

func TestRefsChecksumOrderIndependent(t *testing.T) {
	a := map[string]string{"refs/heads/a": "1111111111111111111111111111111111111111", "refs/heads/b": "2222222222222222222222222222222222222222"}
	b := map[string]string{"refs/heads/b": a["refs/heads/b"], "refs/heads/a": a["refs/heads/a"]}
	if !reflect.DeepEqual(RefsChecksum(a), RefsChecksum(b)) {
		t.Fatal("checksum depends on map order")
	}
}
