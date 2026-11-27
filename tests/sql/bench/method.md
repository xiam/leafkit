# Reproduce the SQL benchmarks

Use the commands in [the README](README.md) from the module root. Use the same
Go toolchain, dependency versions, CPU setting and benchmark flags for every
parser in one comparison. Save repeated raw output privately. No timing value
is a pass/fail threshold or a performance guarantee.

## Matched work

`BenchmarkMatched` measures core, PostgreSQL, MySQL and SQL Server against
identical bytes loaded once from `testdata/`. `matchedFixtures` verifies each
file's SHA-256 against `testdata/matched.json`. The manifest also supplies
expected table names in visitor order. It contains synthetic data only.

- `small.sql` is a single projection and table.
- `joins.sql` includes DISTINCT, qualified names, aliases, an INNER JOIN,
  a comparison and two ordering terms.
- `large.sql` is frozen generator output: 64 projections and four joins.
  Projection index `i` uses `t(i mod 5).c(i) AS col(i)` for `i` from 0 to 63.
  Tables are `bench.table0` through `bench.table4`; each joined table compares
  its `id` to `t0.id`. The query ends with a filter and two ordering terms.
  The checked-in bytes and digest, including final newline, are authoritative.

Before any leaf benchmark timer starts, `checkMatched` checks every parser and
both schedules. It rejects diagnostics, validates the tree, compares
`core.Canonical` results to the core whole-input result, and compares
`core.Tables` results to the manifest. Canonical comparison ignores source
identity and spelling, as defined by `core.Canonical`; it does not prove SQL
engine equivalence. `TestMatched` runs the same checks without benchmarking.
They also run when ordinary tests are skipped with `-run '^$'`.

## Timed work and schedules

`prepare` constructs each engine, compiles its grammar and binds its immutable
source snapshot before timing. All use empty parser state, `parser.Limits{}`,
`parser.Request{}`, the shared trivia policy, and their package's documented
fixed lexical mode. The snapshot and input string are shared read-only across
iterations. Each iteration creates a fresh reader, cursor, scanner, Bytescan
adapter and parser session. No input cursor or parser result is reused.

`runner.parse` includes lexer construction and scanning, adapter setup,
parsing, AST construction, streaming resumes and a final full-file span check.
It consumes one statement, optional semicolon, trailing trivia and final EOF.
`measure` retains the resulting tree in a package sink. Fixture IO, digest
checks, grammar construction, snapshot creation, canonicalization, tree
validation, diagnostic walks and table visits are outside the timed loop.
These are parse benchmarks; they do not time printing or visitor execution.

`whole` exposes all bytes and final EOF immediately. `stream64` starts with
no bytes and pauses, then exposes consecutive 64-byte prefixes to the same
reader/session. The final partial or exact-size chunk includes final EOF.
Both schedules use `feed` and the same snapshot bytes, adapter options and
consumption check. Even the small query exercises an initial streaming pause.
There is no network, disk or sleep in either timed schedule. Compare parsers
within one schedule; a schedule comparison also includes pause/resume costs.

Go reports `ns/op`, `B/op` and `allocs/op` through `ReportAllocs`. `SetBytes`
also reports input throughput. Use `-cpu=1` for a fixed GOMAXPROCS and
`-count=5` or more for repeated samples. These serial benchmarks do not measure
parallel scalability. Do not use race instrumentation for speed measurements;
run the race check separately.

## Separate workloads

`BenchmarkDialectOnly` times one native query for each dialect: PostgreSQL
DISTINCT ON with a dollar string, MySQL STRAIGHT_JOIN with backticks, and SQL
Server TOP WITH TIES with brackets. `checkNative` verifies the typed dialect
node, expected table visits and whole/streamed canonical equivalence first.
Their bytes and semantics differ; do not treat them as matched comparisons.

`BenchmarkRejection` times the same malformed comparison for every parser.
`checkRejection` requires one diagnostic at the semicolon and equivalent whole
and streamed diagnostics within each parser. Expected-token lists may differ
between dialects. Rejection is separate from successful parse comparisons.
`TestSeparateWorkloads` verifies these checks without benchmarking.

## Private provenance

Keep raw benchmark output and the following record together outside public
exports: repository commit, dirty status, corpus manifest and query digests,
Go version, `go env GOOS GOARCH GOAMD64 CGO_ENABLED`, module versions, command
and flags, sample count, UTC interval, CPU model, logical CPU count,
GOMAXPROCS, OS/kernel, memory cap and any competing workload observations.
Record the Flux handle when used. Use one invocation for all matched parsers
so command options agree. On a shared host, report scheduling noise and avoid
claims of isolated hardware. A repeated run is evidence for that environment,
not a guarantee. Public documents contain the method and runnable commands;
measured results belong only in private evidence.
