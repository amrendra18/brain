# Daily schedule

1. Run `scripts/verify fast --feature schedule` on macOS or Linux. The real binary uses owned config, state, home and scheduler fixtures.
2. Preview selects 03:17 local and pins the absolute executable, config and state. File bytes, modes and modification times remain unchanged. Preview cannot contact a scheduler without changing the retained stand-in log.
3. Explicit installation writes the previewed files under the owned home and records its state. The verifier rejects unrelated home changes.
4. Repeating installation returns exit 3. Home and state bytes, modes and modification times remain unchanged.
5. A deliberately failing scheduler executable makes activation fail visibly. Its failed receipt remains available. Retrying without that deliberate failure restores the same schedule identity at 03:18.
6. The PATH contains only owned stand-in executables. No live native scheduler is contacted. Evidence explicitly labels these stand-ins and retains their command log.
7. This proves command wiring and adapter behavior. It does not prove native activation, login behavior, wake behavior or a scheduled invocation.
8. Windows needs a compiled native executable stand-in before this journey can run there. Cross-compilation alone does not prove execution on Windows.
