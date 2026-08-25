package failpoint

import "testing"

func TestOnceAndAlways(t *testing.T) {
	registry := New()
	if err := registry.Set("after_head_cas", Once); err != nil {
		t.Fatal(err)
	}
	if !registry.Hit("after_head_cas") || registry.Hit("after_head_cas") {
		t.Fatal("once failpoint did not fire exactly once")
	}
	if err := registry.Set("drop_all_gossip", Always); err != nil {
		t.Fatal(err)
	}
	if !registry.Hit("drop_all_gossip") || !registry.Hit("drop_all_gossip") {
		t.Fatal("always failpoint did not remain enabled")
	}
}
