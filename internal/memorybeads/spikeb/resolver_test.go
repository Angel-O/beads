package spikeb

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

type resolverFixture struct {
	name     string
	resolver Resolver
	setRoute func(*testing.T, ProjectID, resolverTarget)
}

// resolverTarget is test intent, not a shared provider model. Each fixture
// translates it into its own independently implemented target representation.
type resolverTarget struct {
	projectID ProjectID
	scope     string
	kind      Kind
	access    endpointAccess
	memories  map[BeadID]map[RevisionID]string
}

func TestB2StoreInspectAndExportWithoutResolver(t *testing.T) {
	catalog := NewReferenceCatalog()
	ref := exactForeignReference()
	if err := catalog.Store("source-memory", ref); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got := ResolveStored(context.Background(), nil, ref); got.Status != ResolutionUnconfigured || got.Memory != nil {
		t.Fatalf("nil resolver result = %+v", got)
	}
	if got := catalog.Inspect("source-memory"); !reflect.DeepEqual(got, []Reference{ref}) {
		t.Fatalf("Inspect = %+v", got)
	}
	if got := catalog.Export("source-memory"); !reflect.DeepEqual(got, []Reference{ref}) {
		t.Fatalf("Export = %+v", got)
	}
}

func TestB2ResolverAbsenceDoesNotBreakIndependentMemoryModule(t *testing.T) {
	ctx := context.Background()
	provider := a2.NewIndependentProvider("source-project")
	foreign := a2.Reference{Target: a2.Target{
		ProjectID:     "target-project",
		BeadID:        "target-memory",
		RevisionID:    "target-revision-7",
		ExpectedScope: "project",
		ExpectedKind:  a2.BeadKindMemory,
	}}
	created, err := provider.Mutate(ctx, a2.Mutation{
		Key:        "resolver-independent",
		Title:      "Stored exact foreign reference",
		Body:       "Local Memory Bead behavior does not require a resolver.",
		References: []a2.Reference{foreign},
		Author:     "Ada <ada@example.test>",
	})
	if err != nil || created.Outcome != a2.OutcomeApplied || created.Revision == nil {
		t.Fatalf("Mutate without resolver = %+v, %v", created, err)
	}

	// The optional resolver is absent, and its truthful status does not affect
	// any of the core provider operations below.
	if got := ResolveStored(ctx, nil, exactForeignReference()); got.Status != ResolutionUnconfigured {
		t.Fatalf("nil resolver status = %q", got.Status)
	}
	read, err := provider.Read(ctx, a2.ReadRequest{BeadID: created.Address.BeadID})
	if err != nil || !reflect.DeepEqual(read.References, []a2.Reference{foreign}) {
		t.Fatalf("Read without resolver = %+v, %v", read, err)
	}
	search, err := provider.Search(ctx, a2.SearchRequest{Query: "resolver-independent"})
	if err != nil || len(search.Memories) != 1 || search.Memories[0].BeadID != created.Address.BeadID {
		t.Fatalf("Search without resolver = %+v, %v", search, err)
	}
	history, err := provider.History(ctx, a2.HistoryRequest{BeadID: created.Address.BeadID})
	if err != nil || len(history.Revisions) != 1 || history.Revisions[0].Address != created.Address {
		t.Fatalf("History without resolver = %+v, %v", history, err)
	}
	refs, err := provider.References(ctx, a2.ReferencesRequest{
		BeadID:     created.Address.BeadID,
		RevisionID: created.Address.RevisionID,
	})
	if err != nil || !reflect.DeepEqual(refs.References, []a2.Reference{foreign}) {
		t.Fatalf("References without resolver = %+v, %v", refs, err)
	}
}

func TestB2StructuralValidationFailsBeforeStorage(t *testing.T) {
	base := exactForeignReference()
	cases := []struct {
		name   string
		mutate func(*Reference)
	}{
		{name: "local", mutate: func(ref *Reference) { ref.Local = true; ref.ProjectID = "" }},
		{name: "missing-project", mutate: func(ref *Reference) { ref.ProjectID = "" }},
		{name: "missing-bead", mutate: func(ref *Reference) { ref.BeadID = "" }},
		{name: "missing-revision", mutate: func(ref *Reference) { ref.RevisionID = "" }},
		{name: "floating-marker", mutate: func(ref *Reference) { ref.RevisionID = CurrentRevision }},
		{name: "missing-scope", mutate: func(ref *Reference) { ref.ExpectedScope = "" }},
		{name: "task-kind", mutate: func(ref *Reference) { ref.ExpectedKind = KindTask }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog := NewReferenceCatalog()
			ref := base
			test.mutate(&ref)
			if err := catalog.Store("source", ref); err == nil {
				t.Fatal("Store accepted structurally invalid foreign locator")
			}
			if got := catalog.Inspect("source"); len(got) != 0 {
				t.Fatalf("invalid Store persisted %+v", got)
			}
		})
	}
}

func TestB2ResolverAdaptersShareExactOutcomeContract(t *testing.T) {
	ctx := context.Background()
	for _, fixture := range newResolverFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			ref := exactForeignReference()
			cases := []struct {
				name   string
				target func() resolverTarget
				want   ResolutionStatus
			}{
				{name: "denied", target: func() resolverTarget {
					target := successfulResolverTarget()
					target.access = endpointDenied
					return target
				}, want: ResolutionDenied},
				{name: "unavailable", target: func() resolverTarget {
					target := successfulResolverTarget()
					target.access = endpointUnavailable
					return target
				}, want: ResolutionUnavailable},
				{name: "project-mismatch", target: func() resolverTarget {
					target := successfulResolverTarget()
					target.projectID = "impostor-project"
					return target
				}, want: ResolutionProjectMismatch},
				{name: "scope-mismatch", target: func() resolverTarget {
					target := successfulResolverTarget()
					target.scope = "other-scope"
					return target
				}, want: ResolutionScopeMismatch},
				{name: "kind-mismatch", target: func() resolverTarget {
					target := successfulResolverTarget()
					target.kind = KindTask
					return target
				}, want: ResolutionKindMismatch},
				{name: "missing-exact-revision", target: func() resolverTarget {
					target := successfulResolverTarget()
					target.memories = nil
					return target
				}, want: ResolutionMissingRevision},
				{name: "success", target: successfulResolverTarget, want: ResolutionResolved},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					fixture.setRoute(t, ref.ProjectID, test.target())
					got := fixture.resolver.ResolveExact(ctx, ref)
					if got.Status != test.want {
						t.Fatalf("ResolveExact status = %q, want %q", got.Status, test.want)
					}
					if test.want != ResolutionResolved && got.Memory != nil {
						t.Fatalf("failure disclosed target memory: %+v", got.Memory)
					}
					if test.want == ResolutionDenied && got.Memory != nil {
						t.Fatal("denial leaked body or target existence")
					}
					if test.want == ResolutionResolved {
						if got.Memory == nil || got.Memory.Address != (Address{ProjectID: ref.ProjectID, BeadID: ref.BeadID, RevisionID: ref.RevisionID}) || got.Memory.Body != "exact target body" {
							t.Fatalf("resolved memory = %+v", got.Memory)
						}
					}
				})
			}
		})
	}
}

func TestB2RouteRelocationDoesNotRewriteStoredIdentity(t *testing.T) {
	ctx := context.Background()
	for _, fixture := range newResolverFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			catalog := NewReferenceCatalog()
			ref := exactForeignReference()
			if err := catalog.Store("source", ref); err != nil {
				t.Fatal(err)
			}
			before := catalog.Export("source")
			fixture.setRoute(t, ref.ProjectID, successfulResolverTarget())
			first := fixture.resolver.ResolveExact(ctx, ref)
			relocated := successfulResolverTarget()
			fixture.setRoute(t, ref.ProjectID, relocated)
			second := fixture.resolver.ResolveExact(ctx, ref)
			if first.Status != ResolutionResolved || second.Status != ResolutionResolved {
				t.Fatalf("relocation results: first=%+v second=%+v", first, second)
			}
			if after := catalog.Export("source"); !reflect.DeepEqual(before, after) {
				t.Fatalf("route relocation rewrote stored locator: before=%+v after=%+v", before, after)
			}
		})
	}
}

func newResolverFixtures(t *testing.T) []resolverFixture {
	t.Helper()
	direct := NewRegistryResolver()
	httpResolver := NewHTTPResolver(nil)
	return []resolverFixture{
		{
			name:     "direct-registry",
			resolver: direct,
			setRoute: func(_ *testing.T, projectID ProjectID, target resolverTarget) {
				endpoint := NewProjectEndpoint(target.projectID, target.scope)
				endpoint.Kind = target.kind
				endpoint.Access = target.access
				for beadID, revisions := range target.memories {
					for revisionID, body := range revisions {
						endpoint.AddMemory(beadID, revisionID, body)
					}
				}
				direct.SetRoute(projectID, endpoint)
			},
		},
		{
			name:     "local-http",
			resolver: httpResolver,
			setRoute: func(t *testing.T, projectID ProjectID, target resolverTarget) {
				t.Helper()
				document := NewResolverDocument(target.projectID, target.scope)
				document.RecordType = string(target.kind)
				switch target.access {
				case endpointDenied:
					document.ReadPolicy = documentForbidden
				case endpointUnavailable:
					document.ReadPolicy = documentUnreachable
				}
				for beadID, revisions := range target.memories {
					for revisionID, body := range revisions {
						document.AddMemory(beadID, revisionID, body)
					}
				}
				server := httptest.NewServer(NewResolverDocumentHTTPHandler(document))
				t.Cleanup(server.Close)
				httpResolver.SetRoute(projectID, server.URL)
			},
		},
	}
}

func exactForeignReference() Reference {
	return Reference{
		ProjectID:     "target-project",
		BeadID:        "target-memory",
		RevisionID:    "target-revision-7",
		ExpectedScope: "project",
		ExpectedKind:  KindMemory,
	}
}

func successfulResolverTarget() resolverTarget {
	return resolverTarget{
		projectID: "target-project",
		scope:     "project",
		kind:      KindMemory,
		access:    endpointAllowed,
		memories: map[BeadID]map[RevisionID]string{
			"target-memory": {"target-revision-7": "exact target body"},
		},
	}
}
