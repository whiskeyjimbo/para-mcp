# eval — retrieval regression harness

Owns the per-query-class regression gate for paras retrieval tools (lexical, semantic, hybrid). Runs as part of `go test ./eval/...` and is invoked by CI.

## What it does

- `metrics.go` — pure-Go nDCG@10, recall@10, MRR over a ranked doc-id list.
- `harness.go` — fixture/baseline I/O and the per-class averaging runner.
- `fixtures.json` — checked-in queries + corpus + qrels, grouped by class (`lexical-overlap`, `paraphrase`, `multi-hop`).
- `baseline.json` — recorded per-class metrics; the floor a PR may not drop below by more than `regressionTolerance` (currently 0.05).
- `gate_test.go` — drives the lexical retrieval path through `application.NoteService.Search` and asserts no class regresses.

## Adding a query class or query

1. Append a query block to `fixtures.json`. Set `class` to the appropriate `QueryClass`. Set `relevance` to a `{doc_id: grade}` map (graded relevance is supported but most fixtures use `1`).
2. If you need new corpus docs, add them to `corpus[]`.
3. Run the gate: `go test ./eval/`. A new class will be flagged "missing from current run" against the old baseline; reseed it (next step).

## Updating the baseline

After a deliberate retrieval change (algorithm tuning, fixture growth) the baseline must be regenerated and committed in a dedicated PR:

```
go test ./eval/ -update-baseline
git add eval/baseline.json
git commit -m "test(eval): refresh baseline after <change>"
```

Do not bundle a baseline refresh with code changes — the diff should be reviewable in isolation.

## Limits today

The semantic and hybrid retrieval paths are not yet driven by this gate because the production binary does not wire an Embedder + VectorStore yet (Phase 6a infra exists but is not connected). Once the wiring lands, add a second `Searcher` driving the semantic path and extend the baseline to cover the `paraphrase` and `multi-hop` classes.
