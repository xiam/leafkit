// Package tree builds and inspects ordered trees with optional sharing.
// Example shows hand-built nodes and typed payload access without a parser.
// TestSharedWalks exercises occurrences and identity-based traversal.
//
// Treat published nodes, child accessors and reachable payloads as read-only.
// Keep typed handles to read consumer fields or Value payloads. Pass pointers
// for large structs and arrays. TestPayloadReferences exercises the aliasing
// contract. Rebuild edited paths and reuse valid unchanged nodes, as in
// TestRebuildSharesUnchangedNodes.
//
// Validate custom roots before traversal or printing. Walks and Fprint
// expect a valid root. Positions resolves byte coordinates against an
// external immutable snapshot, as tested in TestPositions.
package tree
