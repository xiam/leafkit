# Writing the documentation

Put information where readers will look for it.

- The root README explains the project in plain language and links to
  more detail. Keep examples short and show their output.
- How-to pages in `docs/` help the reader do a task. Lead with the task and
  the shortest way to complete it. Put runnable Go examples in tests.
- Files in [`docs/specs/`](specs/README.md) record the exact contract. When a
  statement describes current behavior, name the test or source symbol
  that verifies it. Update any prose that restates the contract when you
  change it.

Use short sentences and familiar words. Give each sentence one idea.
Explain technical terms when they first appear or link to their definition.
When a detail moves out of the README, link to its new home.

Keep measured speed figures and benchmark results out of public documents.
Version numbers, examples, positions, test values, and license text can
appear in public documents.

Keep internal URLs out of every file included in the public export.
Root `.gitattributes` lists the exclusions.
`TestPublishedFilesLinkNoInternalHost` checks exported tracked files for the
internal host. Keep private operational notes in `internal-docs/`.
