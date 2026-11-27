package tree

import "iter"

// Step identifies an edge by its parent and zero-based child index.
type Step struct {
	Parent Node
	Index  int
}

// Path lists the edges from the root to an occurrence.
type Path []Step

// Walk visits each occurrence in depth-first, root-first child order.
func Walk(root Node) iter.Seq[Node] {
	return func(yield func(Node) bool) {
		walk(root, func(_ Path, node Node) (bool, bool) {
			return true, yield(node)
		})
	}
}

// WalkPath visits the same occurrences as Walk with their paths. The root has
// an empty path. A path is borrowed until the current yield returns.
func WalkPath(root Node) iter.Seq2[Path, Node] {
	return func(yield func(Path, Node) bool) {
		walk(root, func(path Path, node Node) (bool, bool) {
			return true, yield(path, node)
		})
	}
}

// WalkUnique yields the first occurrence of each ID and prunes subsequent
// visits to that node. Each iteration has its own set of seen IDs.
func WalkUnique(root Node) iter.Seq[Node] {
	return func(yield func(Node) bool) {
		seen := make(map[ID]bool)
		walk(root, func(_ Path, node Node) (bool, bool) {
			id := node.Info().ID
			if seen[id] {
				return false, true
			}
			seen[id] = true
			return true, yield(node)
		})
	}
}

// All yields occurrences whose dynamic type satisfies T.
func All[T any](root Node) iter.Seq[T] {
	return func(yield func(T) bool) {
		for node := range Walk(root) {
			if value, ok := node.(T); ok && !yield(value) {
				return
			}
		}
	}
}

// walk uses the path as its traversal stack. visit controls descent and
// continuation independently, which also lets the printer show repeated edges.
func walk(root Node, visit func(Path, Node) (descend, more bool)) {
	var path Path
	node := root
	for {
		descend, more := visit(path, node)
		if !more {
			return
		}
		if descend {
			if child, ok := node.Child(0); ok {
				path = append(path, Step{Parent: node})
				node = child
				continue
			}
		}
		for {
			if len(path) == 0 {
				return
			}
			last := len(path) - 1
			step := &path[last]
			step.Index++
			if child, ok := step.Parent.Child(step.Index); ok {
				node = child
				break
			}
			path = path[:last]
		}
	}
}
