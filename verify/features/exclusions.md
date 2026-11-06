# Sensitive files and folder ignores

1. Run `scripts/verify fast --feature exclusions` to drive the real binary with owned sources, config, state and home.
2. The synthetic corpus includes all eight new sensitive-name families. Allowed sibling documents must remain searchable with correct citations.
3. Nested ignore rules allow a permitted exception. Root-anchored rules preserve a matching nested sibling. Recursive rules and escaped names exclude their targets.
4. A child rule cannot reopen a blocked directory. An ignore negation cannot reopen the built-in `.ssh` exclusion.
5. A hostile source symlink points to an owned file outside the source folder. Its text must remain unsearchable. The journey requires symlink creation support.
6. Ignore-file content must remain unsearchable. This journey proves search behavior. The named product tests separately observe file opens.
7. The journey first retrieves a permitted document and its heading. It then deliberately adds an ignore rule and explicitly indexes again.
8. Search must omit that document. Showing its old citation must refuse without revealing its old text or heading through messages or recovery arguments.
9. The allowed sibling remains searchable after the rule change. Source, symlink-target and home snapshots must stay unchanged between deliberate fixture edits.
10. Evidence includes command logs and snapshots under `.local/verify/`. Full verification repeats the journey with fresh state.
11. Unit tests use a fixture executable to exercise the journey. Deliberately broken replies expose missing controls, stale text, stale headings and unscoped recovery arguments.
12. Separate broken fixtures modify a source, home, symlink or target. The journey must reject each modification. These tests validate the verifier, not product behavior.
