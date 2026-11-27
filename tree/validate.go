package tree

import (
	"fmt"
	"reflect"
	"strings"
)

type validationEntry struct {
	node     Node
	complete bool
}

type validationFrame struct {
	node Node
	info Envelope
	next int
}

// Validate checks reachable nodes, child references, IDs, spans and cycles.
// Accessors must expose finite, stable sequences. Positions checks bounds
// against the external source bytes when they are needed.
func Validate(root Node) error {
	seen := make(map[ID]validationEntry)
	var stack []validationFrame

	for node := root; ; {
		if err := validatePointer(node); err != nil {
			return err
		}
		info := node.Info()
		if n := len(stack); n > 0 {
			parent := stack[n-1]
			if parent.info.Kind == KindAmbiguity && info.Span != parent.info.Span {
				return fmt.Errorf("tree: alternative %d of node %d has a different span", parent.next-1, parent.info.ID.serial)
			}
		}
		if entry, ok := seen[info.ID]; ok {
			if entry.node != node {
				return fmt.Errorf("tree: distinct nodes have ID %d", info.ID.serial)
			}
			if !entry.complete {
				return fmt.Errorf("tree: cycle at node %d", info.ID.serial)
			}
		} else {
			if err := validateEnvelope(info); err != nil {
				return err
			}
			seen[info.ID] = validationEntry{node: node}
			stack = append(stack, validationFrame{node: node, info: info})
		}

		for {
			if len(stack) == 0 {
				return nil
			}
			last := len(stack) - 1
			frame := &stack[last]
			child, ok := frame.node.Child(frame.next)
			if ok {
				frame.next++
				node = child
				break
			}
			if frame.info.Kind == KindAmbiguity && frame.next < 2 {
				return fmt.Errorf("tree: ambiguity node %d needs at least two alternatives", frame.info.ID.serial)
			}
			seen[frame.info.ID] = validationEntry{node: frame.node, complete: true}
			stack = stack[:last]
		}
	}
}

func validatePointer(node Node) error {
	value := reflect.ValueOf(node)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("tree: expected a non-nil node pointer, got %T", node)
	}
	return nil
}

func validateEnvelope(info Envelope) error {
	switch {
	case info.ID == (ID{}):
		return fmt.Errorf("tree: zero node ID")
	case info.Kind == "":
		return fmt.Errorf("tree: empty kind at node %d", info.ID.serial)
	case strings.HasPrefix(string(info.Kind), "leafkit:") && info.Kind != KindError && info.Kind != KindAmbiguity:
		return fmt.Errorf("tree: reserved kind %q at node %d", info.Kind, info.ID.serial)
	case !info.Span.valid():
		return fmt.Errorf("tree: invalid span at node %d: %+v", info.ID.serial, info.Span)
	}
	return nil
}
