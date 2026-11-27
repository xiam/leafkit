// Package eval folds ordered trees with consumer-defined evaluation rules.
package eval

import "github.com/xiam/leafkit/tree"

// Env holds one scope of runtime bindings. Its zero value is an empty scope.
// An Env must not be copied after first use. Synchronize concurrent access
// if any caller writes to a shared scope.
type Env[V any] struct {
	parent   *Env[V]
	bindings map[string]V
}

// NewEnv creates an empty scope. A nil parent creates a root scope.
func NewEnv[V any](parent *Env[V]) *Env[V] {
	return &Env[V]{parent: parent}
}

// Get returns the nearest binding, or the zero value and false. A nil
// receiver has no bindings.
func (env *Env[V]) Get(name string) (V, bool) {
	for scope := env; scope != nil; scope = scope.parent {
		if value, ok := scope.bindings[name]; ok {
			return value, true
		}
	}
	var zero V
	return zero, false
}

// Set writes to the current scope. The receiver must be non-nil.
func (env *Env[V]) Set(name string, value V) {
	if env.bindings == nil {
		env.bindings = make(map[string]V)
	}
	env.bindings[name] = value
}

// Table holds optional annotations shared by all occurrences of a node.
type Table[V any] map[tree.ID]V
