# B3: cross-project discovery and contribution experiment

**Status:** Executable Beads-wide research. Discovery and contribution are outside the Memory Beads proposal.

## Question

Do cross-project discovery and contribution have separable caller semantics across independently implemented fixture authorities?

## What ran

The direct fixture used `FederationRegistry` and mutable `FederationProject` objects. The HTTP fixture used a separate flat-document `FederationDocumentAuthority` with its own publication path. The HTTP authority did not embed or delegate to the direct fixture authority.

For discovery, both returned only projects registered with their own authority. Summaries contained Project identity, display name, and advertised capabilities but no Memory body. A project could remain discoverable while its separate exact-resolution route denied access, demonstrating that discovery implied neither read nor contribution permission.

For contribution, the same attributed request crossed both adapters. The fixtures exercised known application, application with incomplete verification, pending, rejected, and acknowledgement-uncertain observations. Known publications retained attribution at the owning authority. Every case left a same-ID source record under another Project unchanged.

Run the evidence with:

```bash
go test ./internal/memorybeads/spikeb -run '^TestB3'
go test -race ./internal/memorybeads/spikeb -run '^TestB3'
```

## Current interpretation

Discovery and contribution are independent capabilities; neither follows merely from storing or resolving a foreign reference. The experiment found a candidate semantic separation across two fixtures, not a portable interface or governance model.

It did not select roles, approvals, authentication, cancellation, retry policy, discovery completeness, or production availability. The fixture publication outcomes are not a Memory-specific write contract. Real cross-project providers would need to establish any common Beads-wide contract independently.
