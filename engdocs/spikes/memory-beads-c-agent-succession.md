# Memory Beads C: fresh-agent succession spike

Status: one-run feasibility passed against an isolated `bd`-shaped prototype
on 2026-08-08. This tests the proposed workflow, not production command wiring,
storage, ranking at scale, or behavior across agent models.

## Verdict

A fresh coding agent recovered useful project knowledge without receiving any
memory body in its initial context. It followed a task's explicit reference,
found an unlinked memory through compact search, reported a real absence, and
revised stale guidance without losing history. The evaluator observed exact
Memory Bead and revision citations in its answers.

Selective retrieval was enough for this fixture. The run gives no reason to
inject linked bodies into task output or to load this corpus during priming. It
establishes that the interaction can work; it does not establish a success rate
or prove that every project and agent can work this way.

## Setup

The throwaway command is `cmd/memory-beads-spike-bd`. Its state lives in a
temporary workspace and starts from
`internal/memorybeads/spikec/testdata/succession.json`. The agent was forked
without this conversation's context and was told not to inspect the source,
fixture, generated state, or evaluator telemetry.

It began with the normal-looking entry sequence:

```bash
./bd init --quiet --prefix test --skip-hooks --skip-agents
./bd prime
```

`bd prime` explained task-reference inspection, compact search, exact recall,
search-before-write, optimistic concurrency, and truthful absence. It included
no stored body.

The machine-verifiable run record is
`internal/memorybeads/spikec/testdata/succession-run-evidence.json`. It contains
the 13 events recorded before evaluator inspection, the final state, the later
history check, source and binary hashes, and the task and command sequence
reconstructed from that data. A fresh build produced the recorded binary hash.

The collaboration runner did not retain the exact model identifier, original
assignment text, or verbatim replies. The artifact says so directly and does
not pass reconstructed text off as a transcript. Claims about what the agent
said remain evaluator observations; the event sequence, reads, writes,
attribution, and retained history are independently checkable.

## What happened

The task-linked case took one task read and one exact recall. The agent followed
`task-1` to `mem-policy@rev-policy-1` and returned the repository test policy.
Task output contained the reference metadata, not the policy body.

For unlinked knowledge, the agent searched `storage boundary`, selected the
single compact result, and recalled `mem-storage@rev-storage-1`. When asked to
make sure the project remembered the same fact, it targeted that bead with the
expected current revision. The result was `unchanged`; the provider created no
new bead and no revision.

Three exploratory searches during the absent-policy task (`catering`,
`catering vendor`, and `project events`) returned zero results. The agent said
the corpus did not answer the question and wrote nothing.

The stale-guidance case found `mem-deploy@rev-deploy-1`, recalled its old
`us-west-1` body, and revised the same bead with that exact revision as the
precondition. The new address was `mem-deploy@rev-0031`, attributed to
`Fresh Agent <fresh-agent@example.test>` with the message `Correct preview
deployment region from us-west-1 to us-west-2.` A final exact recall returned
the corrected body while history retained the original revision.

Evaluator telemetry recorded 13 stateful reads or writes:

- one task display;
- six searches: two useful hits, one recoverable overly specific miss, and
  three searches establishing the genuine absence;
- four exact recalls, all relevant to the task at hand;
- one unchanged write attempt and one applied correction.

There were no irrelevant body recalls, missed task links, duplicate beads, or
unnecessary revisions in the recorded state. The evaluator also observed no
fabricated answer, though the missing verbatim replies keep that last judgment
from being independently rechecked.

`TestSuccessionWorkflowContract` builds the real spike executable and repeats
the observable command workflow in a clean workspace. It checks that priming
and task display do not inject a memory body; linked and unlinked exact recall;
complete zero-result search; unchanged writes; non-mutating stale conflicts;
same-bead correction with attribution; parented history; event recording; and
the absence of duplicate beads. A separate test parses the recorded run and
checks its event and final-state invariants. These tests protect the command
contract. They do not simulate an agent or turn a single observed run into a
model evaluation.

## Friction and limits

An unquoted two-word query was parsed as two positional arguments. Quoting it
worked, but the guidance should show a multi-word example. The exact phrase
`preview deployment region` did not match content split across title and body;
the agent recovered by searching `preview`. Production lexical search should
handle useful multi-token matches rather than require one contiguous
substring.

`remember --help` surfaced the standard flag package's `flag: help requested`
error after printing help. That is command polish, not a retrieval-model issue.

This was one agent, one small corpus, and an isolated CLI. It establishes that
the interaction model is workable for the recorded case. It does not measure
ranking quality at scale, production latency, or behavior across model
families. Any specification should cite it as feasibility evidence, not as a
general claim that selective retrieval always succeeds.
