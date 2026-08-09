package spikeb

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type federationFixture struct {
	name                    string
	discovery               Discovery
	contributor             Contributor
	currentSource           func() (RevisionID, string, string)
	currentTarget           func() (RevisionID, string, string)
	setOutcome              func(ContributionOutcome)
	setPublishIndeterminate func(bool)
	deniedTarget            Resolver
}

func TestB3DiscoveryIsExplicitBodyFreeAndNotAuthorization(t *testing.T) {
	ctx := context.Background()
	for _, fixture := range newFederationFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			projects, err := fixture.discovery.Discover(ctx)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(projects) != 3 || projects[0].ProjectID != "denied-project" || projects[1].ProjectID != "source-project" || projects[2].ProjectID != "target-project" {
				t.Fatalf("Discover projects = %+v", projects)
			}
			payload, err := json.Marshal(projects)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), "secret target body") || strings.Contains(string(payload), `"body"`) {
				t.Fatalf("discovery leaked body data: %s", payload)
			}

			denied := exactForeignReference()
			denied.ProjectID = "denied-project"
			if result := fixture.deniedTarget.ResolveExact(ctx, denied); result.Status != ResolutionDenied || result.Memory != nil {
				t.Fatalf("discovered-but-denied project resolved: %+v", result)
			}
		})
	}
}

func TestB3ContributionOutcomesAndProxyIsolation(t *testing.T) {
	ctx := context.Background()
	outcomes := []ContributionOutcome{
		ContributionApplied,
		ContributionAppliedUnverified,
		ContributionPending,
		ContributionRejected,
	}
	for _, outcome := range outcomes {
		for _, fixture := range newFederationFixtures(t) {
			t.Run(fixture.name+"/"+string(outcome), func(t *testing.T) {
				fixture.setOutcome(outcome)
				sourceRevisionBefore, sourceBodyBefore, sourceAuthorBefore := fixture.currentSource()
				result := fixture.contributor.Contribute(ctx, ContributionRequest{
					SourceProjectID:  "source-project",
					TargetProjectID:  "target-project",
					BeadID:           "target-memory",
					ExpectedRevision: "target-rev-0",
					Body:             "owner-side revision",
					Author:           "Ada <ada@example.test>",
				})
				if result.Outcome != outcome {
					t.Fatalf("Contribute outcome = %q", result.Outcome)
				}
				revision, body, author := fixture.currentTarget()
				switch outcome {
				case ContributionApplied, ContributionAppliedUnverified:
					if result.Address == nil || result.Address.ProjectID != "target-project" || result.Address.RevisionID == "" {
						t.Fatalf("known publication lacks canonical address: %+v", result)
					}
					if revision == "target-rev-0" || body != "owner-side revision" {
						t.Fatalf("owner did not publish: revision=%q body=%q", revision, body)
					}
					if author != "Ada <ada@example.test>" {
						t.Fatalf("owner did not persist contribution attribution: %q", author)
					}
				default:
					if result.Address != nil {
						t.Fatalf("non-applied outcome fabricated address: %+v", result.Address)
					}
					if revision != "target-rev-0" || body != "secret target body" || author != "Original owner" {
						t.Fatalf("%q changed owner state: revision=%q body=%q author=%q", outcome, revision, body, author)
					}
				}
				sourceRevisionAfter, sourceBodyAfter, sourceAuthorAfter := fixture.currentSource()
				if sourceRevisionAfter != sourceRevisionBefore || sourceBodyAfter != sourceBodyBefore || sourceAuthorAfter != sourceAuthorBefore {
					t.Fatalf("contribution edited same-ID source proxy: before=%q/%q/%q after=%q/%q/%q", sourceRevisionBefore, sourceBodyBefore, sourceAuthorBefore, sourceRevisionAfter, sourceBodyAfter, sourceAuthorAfter)
				}
			})
		}
	}
}

func TestB3IndeterminateHidesBothPublicationDecisions(t *testing.T) {
	ctx := context.Background()
	for _, publish := range []bool{false, true} {
		for _, fixture := range newFederationFixtures(t) {
			name := fixture.name + "/not-published"
			if publish {
				name = fixture.name + "/published"
			}
			t.Run(name, func(t *testing.T) {
				fixture.setOutcome(ContributionIndeterminate)
				fixture.setPublishIndeterminate(publish)
				result := fixture.contributor.Contribute(ctx, ContributionRequest{
					SourceProjectID: "source", TargetProjectID: "target-project",
					BeadID: "target-memory", ExpectedRevision: "target-rev-0",
					Body: "hidden publication decision", Author: "Ada",
				})
				if result.Outcome != ContributionIndeterminate || result.Address != nil {
					t.Fatalf("indeterminate caller result = %+v", result)
				}
				_, body, author := fixture.currentTarget()
				if gotPublished := body == "hidden publication decision"; gotPublished != publish {
					t.Fatalf("hidden authority publication = %v, want %v", gotPublished, publish)
				}
				if publish && author != "Ada" {
					t.Fatalf("hidden owner publication lost attribution: %q", author)
				}
			})
		}
	}
}

func newFederationFixtures(t *testing.T) []federationFixture {
	t.Helper()
	newRegistry := func() (*FederationRegistry, *FederationProject, *FederationProject) {
		registry := NewFederationRegistry()
		source := NewFederationProject("source-project", "Source project")
		source.Put("target-memory", "proxy-rev-0", "source-side proxy")
		registry.Register(source)
		target := NewFederationProject("target-project", "Target project")
		target.PutAttributed("target-memory", "target-rev-0", "secret target body", "Original owner")
		registry.Register(target)
		registry.Register(NewFederationProject("denied-project", "Visible but denied"))
		// A third project object exists but is not registered; discovery cannot
		// scan it merely because local state exists.
		_ = NewFederationProject("unregistered-project", "Must not be discovered")
		return registry, source, target
	}

	directRegistry, directSource, directTarget := newRegistry()
	directDenied := NewRegistryResolver()
	deniedEndpoint := NewProjectEndpoint("denied-project", "project")
	deniedEndpoint.ProjectID = "denied-project"
	deniedEndpoint.Access = endpointDenied
	directDenied.SetRoute("denied-project", deniedEndpoint)

	documentAuthority := NewFederationDocumentAuthority()
	documentAuthority.AddProject("source-project", "Source project")
	documentAuthority.Put("source-project", "target-memory", "proxy-rev-0", "source-side proxy", "Source owner")
	documentAuthority.AddProject("target-project", "Target project")
	documentAuthority.Put("target-project", "target-memory", "target-rev-0", "secret target body", "Original owner")
	documentAuthority.AddProject("denied-project", "Visible but denied")
	// An independently represented document exists but is not registered with
	// the authority; discovery cannot infer availability from local objects.
	_ = FederationDocument{Identity: "unregistered-project", Label: "Must not be discovered"}
	httpServer := httptest.NewServer(NewFederationDocumentHTTPHandler(documentAuthority))
	t.Cleanup(httpServer.Close)
	httpDeniedDocument := NewResolverDocument("denied-project", "project")
	httpDeniedDocument.ReadPolicy = documentForbidden
	httpDeniedServer := httptest.NewServer(NewResolverDocumentHTTPHandler(httpDeniedDocument))
	t.Cleanup(httpDeniedServer.Close)
	httpDenied := NewHTTPResolver(nil)
	httpDenied.SetRoute("denied-project", httpDeniedServer.URL)

	return []federationFixture{
		{
			name:        "direct-registry",
			discovery:   NewRegistryFederationAdapter(directRegistry),
			contributor: NewRegistryFederationAdapter(directRegistry),
			currentSource: func() (RevisionID, string, string) {
				revision, body := directSource.Current("target-memory")
				return revision, body, directSource.Attribution("target-memory")
			},
			currentTarget: func() (RevisionID, string, string) {
				revision, body := directTarget.Current("target-memory")
				return revision, body, directTarget.Attribution("target-memory")
			},
			setOutcome: func(outcome ContributionOutcome) {
				directTarget.NextOutcome = outcome
			},
			setPublishIndeterminate: func(publish bool) {
				directTarget.PublishIndeterminate = publish
			},
			deniedTarget: directDenied,
		},
		{
			name:        "local-http",
			discovery:   NewHTTPFederationAdapter(httpServer.URL, httpServer.Client()),
			contributor: NewHTTPFederationAdapter(httpServer.URL, httpServer.Client()),
			currentSource: func() (RevisionID, string, string) {
				return documentAuthority.Current("source-project", "target-memory")
			},
			currentTarget: func() (RevisionID, string, string) {
				return documentAuthority.Current("target-project", "target-memory")
			},
			setOutcome: func(outcome ContributionOutcome) {
				documentAuthority.SetOutcome("target-project", outcome)
			},
			setPublishIndeterminate: func(publish bool) {
				documentAuthority.SetPublishIndeterminate("target-project", publish)
			},
			deniedTarget: httpDenied,
		},
	}
}
