# Architecture spikes

These reports capture executable evidence for architecture questions. A passing
prototype proves only the behavior and providers named in its report; it is not
a production implementation or a portability claim.

The [`feat/memory-beads`](https://github.com/gastownhall/beads/tree/feat/memory-beads)
branch is the source of this spike work. Commit
[`dad4ae6f4`](https://github.com/gastownhall/beads/commit/dad4ae6f4)
first completed the remaining executable prototypes. Later commits on the
branch narrow their interpretation and remove experiments that do not support
the Memory product contract; no fixture choice is a production contract.

Memory Beads:

- [A2: revision identity and publication](memory-beads-a2-revisions.md)
- [A3: legacy migration](memory-beads-a3-migration.md)
- [C: fresh-agent succession](memory-beads-c-agent-succession.md)

Related shared-capability research:

- [B1: current-state interchange](memory-beads-b1-interchange.md)
- [B2: exact foreign-reference resolution](memory-beads-b2-pinned-resolution.md)
- [B3: cross-project discovery and contribution](memory-beads-b3-optional-federation.md)
- [Floating foreign heads](memory-beads-floating-foreign-heads.md)
