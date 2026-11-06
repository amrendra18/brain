# Content privacy and old-state replacement

1. Run `scripts/verify fast --feature privacy` to drive the real binary with owned sources, configuration, state and home.
2. The source fixture contains a constructed token and two literal values. The first policy protects one literal. Allowed surrounding words retain exact document citations.
3. The selected private-values file uses a document extension inside the source folder. A database check requires that control file to remain outside the index.
4. Search, citation lookup, long saved notes, oversized-note help, project maps and doctor replies must omit protected values. Citation text retains the named redaction markers.
5. Doctor reports relative affected paths and the labels applied to replacements. Overlapping matches can share one selected label. Successful redaction alone keeps doctor healthy.
6. The verifier reads logical document, occurrence, chunk, retired-heading and full-text tables. It also scans every regular state file for protected fixture bytes.
7. The next policy protects the second literal without changing document bytes. Search must refuse until explicit indexing replaces the older state. Allowed saved notes remain searchable afterward.
8. On non-Windows hosts, current private state keeps readable answers when its folder is read-only. Changed-policy read-only state must refuse. The evidence names the unrun Windows mode-bit checks.
9. A deliberate legacy fixture mutation removes the stored fingerprint and inserts raw text into database records, a converted cache file and an owned note. The scanner must first detect that seeded text.
10. Explicit indexing must replace that old state. All remaining state files and logical tables must omit the protected bytes. Allowed source citations and each saved note remain present exactly once.
11. A successful migration leaves no migration marker or staging folder. This checks remaining application-owned files, without claiming erasure of storage blocks.
12. Source, selected configuration and home bytes remain unchanged between the verifier's deliberate policy edits. Command logs and scan counts remain under `.local/verify/`.
13. Unit tests use a fixture executable and deliberately broken replies, leaked bytes, lost notes, duplicate notes and retained migration artifacts. These checks validate the verifier. Real-binary evidence proves product behavior.
14. `TestKilledIndexLeavesOldIndex` and `TestNoteLockAcrossProcesses` cover interruption and concurrent note writers. This journey does not simulate power loss or native Windows access controls.

15. The note journey submits more than 2,000 bytes in one call. It requires one dated entry, a searchable final chunk and unchanged allowed text after migration. Input beyond 65,536 bytes refuses safely without copied-text retry commands.
