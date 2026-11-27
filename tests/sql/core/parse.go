package core

import (
	"fmt"
	"strings"

	"github.com/xiam/bytescan/cursor"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// Engine compiles a fresh grammar and binds construction to one snapshot.
// For a dialect, configure Grammar/Scanner/Builders directly with public APIs.
func Engine(snapshot *Snapshot) (*p.Engine[tree.Node], error) {
	return p.Configure(Grammar(), Builders(snapshot, nil), p.Limits{})
}

// Parse accepts one complete SELECT and an optional semicolon. Invalid syntax
// is returned as the parser's diagnostic tree; inspect Diagnostics before use.
func Parse(snapshot *Snapshot) (tree.Node, error) {
	engine, err := Engine(snapshot)
	if err != nil {
		return nil, err
	}
	cur := cursor.NewReader(strings.NewReader(snapshot.Text()))
	source, err := adapter.New(snapshot.ID(), Scanner(cur), cur, p.Request{}, Trivia())
	if err != nil {
		return nil, err
	}
	root, status, err := engine.Start(source, "").Run()
	if err == nil && status != p.Done {
		return nil, fmt.Errorf("sql: complete input paused")
	}
	return root, err
}

// Diagnostics returns diagnostic leaves in source traversal order. The core
// does not repair statements. A valid prefix can precede a trailing error;
// always check the entire file before printing or consuming its SQL nodes.
func Diagnostics(root tree.Node) []*p.ErrorNode {
	var result []*p.ErrorNode
	if root == nil {
		return result
	}
	for node := range tree.Walk(root) {
		if e, ok := node.(*p.ErrorNode); ok {
			result = append(result, e)
		}
	}
	return result
}

// Tables visits relation references, including joined tables, in syntax order.
// It uses shared Table nodes through the public tree visitor and needs no
// dialect switch. Aliases and expression qualifiers are not table references.
func Tables(root tree.Node) []*Table {
	var result []*Table
	if root == nil {
		return result
	}
	for node := range tree.Walk(root) {
		if table, ok := node.(*Table); ok {
			result = append(result, table)
		}
	}
	return result
}
