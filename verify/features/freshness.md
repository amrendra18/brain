# Stored-index freshness

1. Run `scripts/verify fast --feature freshness` against the real binary with temporary config, state and source files.
2. The journey indexes two sources, changes one document and adds another. Ordinary reads retain the earlier citation and leave new text absent.
3. Search, show, projects, recent, map, status, doctor and the three-line brief run without `--no-refresh`.
4. Saving a note makes that note immediately searchable. Unrelated indexed documents remain searchable. The full-source index age does not reset.
5. An explicit `index` reveals changed and added source text. It removes the old changed text from results and preserves the saved note.
6. Source and home snapshots, command replies and exits remain under `.local/verify/`. All deliberate source edits occur before the source snapshot.
7. The journey proves observed results. `TestFreshnessReadsNeverOpenSources` observes zero source opens through instrumented file APIs. `TestFreshnessObserverRejectsDiscardedScans` detects deliberately added scans despite unchanged replies. Explicit indexing supplies the positive document-open control.
8. Deliberately broken fixture replies, automatic indexing during note creation and source writes test the verifier. They are test inputs, not product evidence.
