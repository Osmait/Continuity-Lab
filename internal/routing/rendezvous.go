package routing

import (
	"bytes"
	"crypto/sha256"
	"sort"
)

type Node struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type scored struct {
	node  Node
	score [32]byte
}

func Rank(repoID string, nodes []Node) []Node {
	items := make([]scored, 0, len(nodes))
	for _, node := range nodes {
		sum := sha256.Sum256(append(append([]byte(repoID), 0), []byte(node.ID)...))
		items = append(items, scored{node: node, score: sum})
	}
	sort.Slice(items, func(i, j int) bool {
		comparison := bytes.Compare(items[i].score[:], items[j].score[:])
		if comparison == 0 {
			return items[i].node.ID < items[j].node.ID
		}
		return comparison > 0
	})
	result := make([]Node, len(items))
	for i := range items {
		result[i] = items[i].node
	}
	return result
}
