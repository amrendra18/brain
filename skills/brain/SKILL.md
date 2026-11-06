---
name: brain
description: Search selected project files before answering questions about those projects or past decisions.
---

# Brain

Search local Markdown, DOCX and RTF files before answering about past work. Cite the returned file path.

Put brain (brain.exe on Windows) on your command path. These commands work in every shell.
Add --format json for one compact JSON reply. No model or network is used. Run index only for an explicit update or an unusable index after other routes failed.

## search

Find chunks with cited paths. Use two or three specific keywords, such as lease timeout. Reads use the stored index without scanning sources. Default limit: 8.
```
brain search "lease timeout" --format json
```

## show

Read one chunk using an id from search, with its neighbouring chunk ids.
```
brain show c2d5dfe26ef438b2 --format json
```

## projects

Using the stored index, list project folders, README purpose, file counts and last-indexed times.
```
brain projects --format json
```

## recent

Using the stored index, list current files newest first. Default days: 7. Default limit: 20.
```
brain recent --days 3 --format json
```

## note

Save one dated note under the state folder, then index only that note. Sources are not scanned.
```
brain note "The lease timeout stays at 30 minutes" --project alpha --format json
```

## map

Using the stored index, show one project page of fewer than 60 lines.
```
brain map alpha --format json
```

## Reply rules

- Every reading reply carries index_age_s. It reports the index age in seconds. index_complete says whether a complete source scan has finished.
- Run the needed next command exactly as printed. next_argv runs without a shell. After a refusal, the first command gives recovery guidance.

For other commands and flags, run `brain help COMMAND`.
