# Memory Beads C: fresh-agent succession experiment

**Status:** One observed workflow run plus an executable command-contract fixture, recorded 2026-08-08. This is feasibility evidence, not a model-wide success claim.

## Question

Can a fresh coding agent recover and maintain useful project knowledge through compact discovery, explicit references, and complete recall without receiving Memory bodies in its initial context?

## Setup

The throwaway command is `cmd/memory-beads-spike-bd`. Its temporary state starts from `internal/memorybeads/spikec/testdata/succession.json`.

The agent was forked without the originating conversation and was told not to inspect the prototype source, fixture, generated state, or evaluator telemetry. It began with:

```bash
./bd init --quiet --prefix test --skip-hooks --skip-agents
./bd prime
```

Priming described task-reference inspection, compact search, explicit recall, search-before-write, common guarded-write behavior, and truthful absence. It included no stored body.

The current command-contract fixture additionally teaches the agent to keep
short-lived progress and scratch state with the task or runtime, and to use
`remember` only for deliberately chosen project knowledge that should outlast
the current work. This wording postdates the recorded agent run and is covered
by the regression test rather than claimed as evidence from that run.

The machine-verifiable record is `internal/memorybeads/spikec/testdata/succession-run-evidence.json`. It contains 13 recorded stateful events, final state, a later history check, source and binary hashes, and a reconstructed task and command sequence. The runner did not retain the exact model identifier, original assignment text, or verbatim replies, and the artifact does not present reconstructed text as a transcript.

## Observed run

The agent:

- followed one task reference and explicitly recalled the linked policy body;
- found an unlinked storage-boundary Memory through compact search and recall;
- attempted to store the same durable fact and received an unchanged result with no new fixture version;
- made three searches for absent catering policy, reported that the corpus did not answer, and wrote nothing; and
- found stale deployment guidance, recalled it, updated the same Memory through the fixture's guarded-write path, and verified both the corrected current state and retained earlier state.

The recorded fixture addresses included `mem-policy@rev-policy-1`, `mem-storage@rev-storage-1`, and `mem-deploy@rev-deploy-1`; these are test data, not a proposed Historical Bead Reference format. The correction was attributed to `Fresh Agent <fresh-agent@example.test>`.

Telemetry recorded one task display, six searches, four relevant recalls, one unchanged write attempt, and one applied correction. It recorded no irrelevant recall, missed task link, duplicate Memory, or unnecessary version. The evaluator observed no fabricated answer, but the missing verbatim replies prevent independent rechecking of that judgment.

`TestSuccessionWorkflowContract` rebuilds the spike executable and repeats the command workflow in a clean workspace. It checks absence of body injection, linked and unlinked recall, complete zero-result search, unchanged writes, non-mutating stale conflicts, same-Bead correction with attribution, retained history, event recording, and absence of duplicates. A separate test checks the recorded run's event and final-state invariants.

## Current interpretation

Selective retrieval was sufficient for this small recorded case. The result supports keeping task display and priming body-free while allowing explicit complete recall. It does not establish a general success rate, ranking quality at scale, production latency, or behavior across model families.

The prototype's revision strings and guarded-write mechanics are not the Memory product model. Production Memory Beads inherits historical addressing and mutation behavior from shared Beads History and Versioned Bead contracts.

## Friction observed

The agent had to quote a multi-word query, and one useful phrase did not match content split across title and body until the query was shortened. `remember --help` also printed the standard flag package's help-requested error. These are prototype usability observations, not evidence against selective retrieval.
