# Memory Beads: floating foreign head experiment

Status: executable model comparison. Exact foreign pins remain the selected
portable v1 rule; floating foreign resolution is not a v1 capability.

## Verdict

An owner-designated model can select a current foreign head, but only after the
caller receives a verified designation for the expected Project ID and the
designated route proves identity, freshness, and a non-forked current head.
The executable success path demonstrates that route order and replica voting
are unnecessary under those conditions.

A synchronization/consensus model is insufficient on its own. Synchronized,
fresh replicas can agree on an opaque head yet still fail to prove which
provider is entitled to define current state. Disagreement, missing
synchronization, stale evidence, forks, identity mismatch, unavailability, and
denial all fail closed.

No production provider in this spike supplies and verifies the required owner
designation, freshness evidence, and fork contract. Therefore exact pins, not
floating heads, remain the portable v1 choice. This result does not make a
later owner-designated capability infeasible.

## Compared models

`CompareFloatingModels` evaluates identical observations with two rules:

1. **Owner-designated:** only a route named by a verified owner-authority proof
   may select the head. A replica cannot outvote or replace it. A matching,
   fresh, non-forked owner observation succeeds and returns its opaque head.
2. **Synchronized consensus:** routes must prove synchronization and freshness
   and agree on the opaque head. Even complete agreement returns
   `owner_authority_unproved` because synchronization is not ownership.

The authority proof is an input to the model, not a newly invented wire format
or trust algorithm. A future provider experiment must show how that proof is
obtained and verified across relocation before exposing floating resolution.

## Executable evidence

The comparison exercises:

- both route-order permutations with a designated owner and a disagreeing
  replica: the owner model selects the same owner head, while consensus remains
  ambiguous;
- a stale designated owner, which cannot be replaced by a newer-looking
  replica;
- competing owner heads, reported as a fork;
- a designated route reporting the wrong Project ID;
- synchronized replicas agreeing without any owner proof;
- a resolved route without synchronization evidence; and
- owner denial alongside a revealing replica in both route orders, with no
  fallback and no leaked head.

Run the evidence with:

```bash
go test ./internal/memorybeads/spikeb -run '^TestFloating'
go test -race ./internal/memorybeads/spikeb -run '^TestFloating'
```

## Conditions for reconsidering floating heads

A later owner-designated capability can work if a real provider supplies a
verifiable Project-owner designation that survives route relocation, the
designated authority reports freshness under a documented contract, forks have
an explicit non-silent outcome, and denial never falls through to a revealing
replica. Two production implementations should exercise those semantics before
they become a portable contract. Until then, a resolver requires the stored
exact revision.
