# MySQL SELECT example

Parse a MySQL SELECT with the shared SQL grammar and an ordered join:

```go
root, err := mysql.Parse(core.NewSnapshot("query",
    "SELECT `u`.`id` FROM users AS u STRAIGHT_JOIN teams AS t ON u.team_id = t.id"))
```

Check the Go error and `core.Diagnostics(root)` before using the tree.
`mysql.Print(root)` retains the join instruction. `core.Tables(root)` finds
both tables. See `ExampleParse` for runnable code and output.

Read the [local contract](contract.md) for the fixed lexical mode, supported
join form, and test oracles. The [shared core](../core/README.md) describes
ordinary SELECT syntax.
