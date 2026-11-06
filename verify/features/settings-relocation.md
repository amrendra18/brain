# Saved settings and relocation

1. Run `scripts/verify full --feature settings-relocation` to repeat the real-binary journey with two fresh temporary homes.
2. The verifier remembers an explicit state and external configuration, then launches a fresh process without scope flags.
3. The verifier saves one note and checks that relocation preview changes no files.
4. The verifier moves data into a folder with spaces and checks the fresh process selects it.
5. The verifier compares original and moved note bytes and preserves the external configuration and source hashes.
6. Search must cite the moved note. A stale command using the previous state must refuse without changing the retained note.
7. This journey does not prove native scheduling or clean-checkout installation.
