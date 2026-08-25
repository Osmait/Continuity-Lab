package routing

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestRankStableAndOrderIndependent(t *testing.T) {
	nodes := []Node{{ID: "node-a"}, {ID: "node-b"}, {ID: "node-c"}}
	reversed := []Node{nodes[2], nodes[0], nodes[1]}
	a, b := Rank("repo-fixture", nodes), Rank("repo-fixture", reversed)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("ranking depends on input order: %#v != %#v", a, b)
	}
	got := []string{a[0].ID, a[1].ID, a[2].ID}
	want := []string{"node-b", "node-c", "node-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("golden ranking changed: got %v want %v", got, want)
	}
}

func TestRankRemovalOnlyMovesPreferredRepos(t *testing.T) {
	all := []Node{{ID: "node-a"}, {ID: "node-b"}, {ID: "node-c"}}
	remaining := []Node{{ID: "node-a"}, {ID: "node-c"}}
	for i := 0; i < 1000; i++ {
		repo := string(rune(i))
		before, after := Rank(repo, all), Rank(repo, remaining)
		if before[0].ID != "node-b" && before[0].ID != after[0].ID {
			t.Fatalf("repo %d moved despite another preferred node", i)
		}
	}
}

func BenchmarkRankThreeNodes(b *testing.B) {
	nodes := []Node{{ID: "node-a"}, {ID: "node-b"}, {ID: "node-c"}}
	repos := make([]string, 1024)
	for i := range repos {
		repos[i] = fmt.Sprintf("%064x", i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Rank(repos[i%len(repos)], nodes)
	}
}

func TestRankDistribution(t *testing.T) {
	nodes := []Node{{ID: "node-a"}, {ID: "node-b"}, {ID: "node-c"}}
	counts := map[string]int{}
	const total = 100000
	for i := 0; i < total; i++ {
		counts[Rank(fmt.Sprintf("%064x", i), nodes)[0].ID]++
	}
	want := float64(total) / 3
	for id, count := range counts {
		if math.Abs(float64(count)-want) > want*0.03 {
			t.Fatalf("distribution for %s is %d", id, count)
		}
	}
}
