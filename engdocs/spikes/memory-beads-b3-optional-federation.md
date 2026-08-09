# Memory Beads B3: optional discovery and contribution spike

Status: executable internal prototype. Two independently implemented fixture
providers satisfy the same discovery and contribution caller needs. The real
production-adapter gate remains open.

## Verdict

Discovery and contribution are distinct capabilities. Neither belongs on the
core Memory Module merely because the other exists.

The experiment found a small common semantic shape across two independent
fixture providers, not two production integrations. Keep these seams internal
until real adapters with their own identity and governance demonstrate the
same need.

## Independent provider evidence

The direct provider uses `FederationRegistry` and mutable
`FederationProject` objects. The HTTP provider uses a separate
`FederationDocumentAuthority`: projects are flat documents, memories are
document entries, and publication has an independently implemented outcome
path. The HTTP authority neither embeds the registry nor delegates to
`FederationProject`.

This distinction matters because both providers now arrive at the same caller
outcomes without sharing fixture authority or target storage code. They still
remain fixtures, so this result does not satisfy the architecture's requirement
for two real adapters before defining a public compatibility surface.

## Discovery evidence

Both providers return only projects explicitly registered with their own
authority. Each summary contains Project ID, display name, and advertised
capabilities; it contains no memory body. A locally constructed but
unregistered project is absent.

The test advertises a project whose separate exact-resolution route denies
access. It remains discoverable while resolution returns a nonleaking denial.
Discovery therefore implies neither read access nor contribution authority.

## Contribution evidence

The same attributed request runs through the direct and HTTP adapters. The
target owner produces each portable outcome:

- `applied` and `applied_unverified` publish at the owner and return the known
  canonical address;
- `pending` and `rejected` publish nothing and return no address; and
- `indeterminate` returns no address whether the hidden authority published or
  did not publish.

For every known publication, each independent owner persists the request's
author with the new revision and the tests read that attribution back from the
owner. The indeterminate-published case also preserves attribution even though
the caller cannot know whether publication occurred.

The source and target deliberately contain the same Bead ID under different
Project IDs. Every outcome leaves the source-owned revision, body, and
attribution unchanged, so the proxy-isolation check cannot pass merely because
the IDs differ.

Run the evidence with:

```bash
go test ./internal/memorybeads/spikeb -run '^TestB3'
go test -race ./internal/memorybeads/spikeb -run '^TestB3'
```

## Limits and remaining gate

The spike does not select roles, approval rules, authentication, proposal
expiry, cancellation, retry policy, or discovery completeness beyond explicit
provider publication. The HTTP path proves an independent fixture authority
and real transport boundary, not deployed governance. Freeze a shared
Discovery or Contribution interface only after two real adapters independently
need it; otherwise keep each capability provider-specific.
