package spikeb

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

type interchangeFactory struct {
	name string
	new  func(*testing.T, ProjectID) InterchangeProvider
}

func interchangeFactories() []interchangeFactory {
	return []interchangeFactory{
		{
			name: "append-only-a2-adapter",
			new: func(_ *testing.T, projectID ProjectID) InterchangeProvider {
				return NewA2InterchangeProvider(projectID)
			},
		},
		{
			name: "transactional-json-document",
			new: func(t *testing.T, projectID ProjectID) InterchangeProvider {
				t.Helper()
				provider, err := NewDocumentInterchangeProvider(projectID, filepath.Join(t.TempDir(), "PROTOTYPE-memory-beads-b1.json"))
				if err != nil {
					t.Fatalf("NewDocumentInterchangeProvider: %v", err)
				}
				return provider
			},
		},
	}
}

func TestB1BidirectionalRoundTripAndSelfImport(t *testing.T) {
	ctx := context.Background()
	factories := interchangeFactories()
	for _, direction := range []struct {
		name         string
		source, dest interchangeFactory
	}{
		{name: "A2-to-document", source: factories[0], dest: factories[1]},
		{name: "document-to-A2", source: factories[1], dest: factories[0]},
	} {
		t.Run(direction.name, func(t *testing.T) {
			source := direction.source.new(t, "source-project")
			seedCompactGraph(t, ctx, source)
			before, err := source.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			unit, err := source.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}

			wire, err := EncodeInterchange(unit)
			if err != nil {
				t.Fatalf("encode source interchange: %v", err)
			}
			unit, err = DecodeInterchange(wire)
			if err != nil {
				t.Fatalf("decode destination interchange: %v", err)
			}

			destination := direction.dest.new(t, "destination-project")
			result := importUnit(t, ctx, destination, unit)
			if result.Outcome != ImportApplied || len(result.Mapping) != len(unit.Records) {
				t.Fatalf("import result = %+v", result)
			}
			after, err := destination.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := semanticGraph(after), semanticGraph(before); !reflect.DeepEqual(got, want) {
				t.Fatalf("semantic graph mismatch\n got: %#v\nwant: %#v", got, want)
			}
			beforeByTitle := recordsByTitle(before)
			afterByTitle := recordsByTitle(after)
			for title, sourceRecord := range beforeByTitle {
				if sourceRecord.Kind != KindMemory {
					continue
				}
				destinationRecord := afterByTitle[title]
				if destinationRecord.Author != harnessBImportAuthor || destinationRecord.Origin != "canonical_import" {
					t.Fatalf("destination revision attribution for %q = author %q origin %q", title, destinationRecord.Author, destinationRecord.Origin)
				}
				if len(destinationRecord.Provenance) == 0 {
					t.Fatalf("destination revision %q lost source evidence", title)
				}
				evidence := destinationRecord.Provenance[len(destinationRecord.Provenance)-1]
				if evidence.ProjectID != unit.SourceProjectID || evidence.BeadID != sourceRecord.ID || evidence.RevisionID != sourceRecord.RevisionID || evidence.Author != sourceRecord.Author || evidence.Origin != sourceRecord.Origin {
					t.Fatalf("destination source evidence for %q = %+v, source=%+v", title, evidence, sourceRecord)
				}
			}

			// Producer self-import is a true no-op, not a duplicate graph.
			selfUnit, err := destination.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selfWire, err := EncodeInterchange(selfUnit)
			if err != nil {
				t.Fatal(err)
			}
			selfUnit, err = DecodeInterchange(selfWire)
			if err != nil {
				t.Fatal(err)
			}
			selfPlan, err := destination.PrepareImport(ctx, selfUnit)
			if err != nil {
				t.Fatalf("prepare self import: %v", err)
			}
			selfResult, err := destination.ApplyImport(ctx, selfPlan)
			if err != nil {
				t.Fatalf("apply self import: %v", err)
			}
			if selfResult.Outcome != ImportUnchanged {
				t.Fatalf("self import outcome = %q", selfResult.Outcome)
			}
			selfAfter, _ := destination.Snapshot(ctx)
			if !reflect.DeepEqual(after, selfAfter) {
				t.Fatal("self import changed destination state")
			}
		})
	}
}

func TestB1WireIsStrictAndDeclarationFirst(t *testing.T) {
	unit := compactGraphUnit()
	wire, err := EncodeInterchange(unit)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInterchange(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, unit) {
		t.Fatalf("wire round trip changed unit\n got: %#v\nwant: %#v", decoded, unit)
	}
	firstLine := bytes.SplitN(wire, []byte{'\n'}, 2)[0]
	if !bytes.Contains(firstLine, []byte(`"_type":"memory_beads_interchange"`)) || !bytes.Contains(firstLine, []byte(`"id":{"reject_legacy":true}`)) {
		t.Fatalf("first line lacks declaration/downgrade guard: %s", firstLine)
	}

	unknown := bytes.Replace(wire, []byte(`"source_project_id"`), []byte(`"unknown":true,"source_project_id"`), 1)
	if _, err := DecodeInterchange(unknown); err == nil {
		t.Fatal("decoder accepted an unknown declaration field")
	}
	unguarded := bytes.Replace(wire, []byte(`"reject_legacy":true`), []byte(`"reject_legacy":false`), 1)
	if _, err := DecodeInterchange(unguarded); !errors.Is(err, ErrUnsupportedDeclaration) {
		t.Fatalf("unguarded decode error = %v", err)
	}
}

func TestB1DeclarationGuardsRejectBeforeRecords(t *testing.T) {
	ctx := context.Background()
	for _, factory := range interchangeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			base := compactGraphUnit()
			cases := []struct {
				name   string
				mutate func(*InterchangeUnit)
			}{
				{name: "old-format", mutate: func(unit *InterchangeUnit) { unit.Declaration.Format = "legacy-kv" }},
				{name: "unknown-format", mutate: func(unit *InterchangeUnit) { unit.Declaration.Format = "memory-beads-future" }},
				{name: "unknown-version", mutate: func(unit *InterchangeUnit) { unit.Declaration.Version = "b1-v99" }},
				{name: "unknown-scope", mutate: func(unit *InterchangeUnit) { unit.Declaration.Scope = "all-history" }},
				{name: "unknown-capability", mutate: func(unit *InterchangeUnit) {
					unit.Declaration.RequiredCapabilities = append(unit.Declaration.RequiredCapabilities, "telepathy")
				}},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					provider := factory.new(t, "destination")
					unit := cloneUnit(base)
					test.mutate(&unit)
					if _, err := provider.PrepareImport(ctx, unit); !errors.Is(err, ErrUnsupportedDeclaration) {
						t.Fatalf("PrepareImport error = %v", err)
					}
					records, _ := provider.Snapshot(ctx)
					if len(records) != 0 {
						t.Fatalf("guard failure applied %d records", len(records))
					}
				})
			}
		})
	}

	writes := 0
	if err := LegacyImport(compactGraphUnit(), func(Record) error { writes++; return nil }); !errors.Is(err, ErrLegacyRejected) {
		t.Fatalf("LegacyImport error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("legacy importer applied %d records before rejecting declaration", writes)
	}
}

func TestB1LocatorMappingBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, factory := range interchangeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			destination := factory.new(t, "destination")
			unit := compactGraphUnit()
			result := importUnit(t, ctx, destination, unit)
			records, _ := destination.Snapshot(ctx)
			byTitle := recordsByTitle(records)
			policy := byTitle["Policy"]
			task := byTitle["Implement policy"]
			guide := byTitle["Guide"]
			archived := byTitle["Archived note"]

			assertMappedCurrent(t, task.References[0], policy)
			mappedGuide := findReference(t, guide.References, func(ref Reference) bool {
				return ref.Local && ref.BeadID == policy.ID
			})
			assertMappedCurrent(t, mappedGuide, policy)
			foreign := findReference(t, guide.References, func(ref Reference) bool {
				return !ref.Local && ref.ProjectID == "foreign-project"
			})
			if foreign.Local || foreign.ProjectID != "foreign-project" || foreign.BeadID != "foreign-memory" || foreign.RevisionID != "foreign-rev-9" {
				t.Fatalf("foreign exact locator changed: %+v", foreign)
			}
			historical := archived.References[0]
			if historical.Local || historical.ProjectID != unit.SourceProjectID || historical.BeadID != "policy" || historical.RevisionID != "policy-rev-old" {
				t.Fatalf("historical source pin retargeted: %+v", historical)
			}
			if result.Mapping["policy"] != policy.ID {
				t.Fatalf("mapping policy = %q, record ID %q", result.Mapping["policy"], policy.ID)
			}

			// A same-ID destination record is not evidence of source identity.
			existingID := policy.ID
			externalPin := RevisionID("source-exact-revision")
			referenceOnly := InterchangeUnit{
				Declaration:     CanonicalDeclaration(),
				SourceProjectID: "another-source",
				Records: []Record{{
					ID:    "source-task",
					Kind:  KindTask,
					Title: "Reference existing-looking ID",
					Body:  "No retargeting",
					References: []Reference{{
						Local: true, BeadID: existingID, RevisionID: externalPin,
						ExpectedScope: "project", ExpectedKind: KindMemory,
					}},
				}},
			}
			importUnit(t, ctx, destination, referenceOnly)
			after, _ := destination.Snapshot(ctx)
			importedTask := recordsByTitle(after)["Reference existing-looking ID"]
			got := importedTask.References[0]
			if got.Local || got.ProjectID != "another-source" || got.BeadID != existingID || got.RevisionID != externalPin {
				t.Fatalf("same-ID destination silently captured source locator: %+v", got)
			}

			floating := cloneUnit(referenceOnly)
			floating.Records[0].ID = "floating-task"
			floating.Records[0].Title = "Unmapped floating"
			floating.Records[0].References[0].RevisionID = ""
			beforeFloating, _ := destination.Snapshot(ctx)
			if _, err := destination.PrepareImport(ctx, floating); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unmapped floating PrepareImport error = %v", err)
			}
			afterFloating, _ := destination.Snapshot(ctx)
			if !reflect.DeepEqual(beforeFloating, afterFloating) {
				t.Fatal("rejected floating locator changed destination")
			}
		})
	}
}

func TestB1ConflictRaceAndFailureAreAtomic(t *testing.T) {
	ctx := context.Background()
	for _, factory := range interchangeFactories() {
		t.Run(factory.name, func(t *testing.T) {
			t.Run("key-conflict", func(t *testing.T) {
				destination := factory.new(t, "destination")
				seed := singleMemoryUnit("seed", "owned", "collision")
				importUnit(t, ctx, destination, seed)
				before, _ := destination.Snapshot(ctx)
				conflict := singleMemoryUnit("incoming", "new", "collision")
				if _, err := destination.PrepareImport(ctx, conflict); !errors.Is(err, ErrConflict) {
					t.Fatalf("key conflict error = %v", err)
				}
				after, _ := destination.Snapshot(ctx)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("key conflict partially changed destination")
				}
			})

			t.Run("destination-race", func(t *testing.T) {
				destination := factory.new(t, "destination")
				plan, err := destination.PrepareImport(ctx, compactGraphUnit())
				if err != nil {
					t.Fatal(err)
				}
				importUnit(t, ctx, destination, singleMemoryUnit("racer", "racer", "racer-key"))
				before, _ := destination.Snapshot(ctx)
				if _, err := destination.ApplyImport(ctx, plan); !errors.Is(err, ErrStaleDestination) {
					t.Fatalf("raced ApplyImport error = %v", err)
				}
				after, _ := destination.Snapshot(ctx)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("stale plan partially applied connected graph")
				}
			})

			t.Run("injected-before-publish", func(t *testing.T) {
				destination := factory.new(t, "destination")
				plan, err := destination.PrepareImport(ctx, compactGraphUnit())
				if err != nil {
					t.Fatal(err)
				}
				destination.FailBeforePublishOnce()
				if _, err := destination.ApplyImport(ctx, plan); !errors.Is(err, ErrInjectedFailure) {
					t.Fatalf("injected ApplyImport error = %v", err)
				}
				records, _ := destination.Snapshot(ctx)
				if len(records) != 0 {
					t.Fatalf("injected failure exposed %d partial records", len(records))
				}
				// The same validated unit can be retried from a fresh preflight.
				importUnit(t, ctx, destination, compactGraphUnit())
				records, _ = destination.Snapshot(ctx)
				if len(records) != len(compactGraphUnit().Records) {
					t.Fatalf("retry stored %d records", len(records))
				}
			})
		})
	}
}

func TestB1DocumentProviderCASSpansIndependentInstances(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared-project.json")
	first, err := NewDocumentInterchangeProvider("destination", path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDocumentInterchangeProvider("destination", path)
	if err != nil {
		t.Fatal(err)
	}
	firstPlan, err := first.PrepareImport(ctx, singleMemoryUnit("source-a", "a", "key-a"))
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := second.PrepareImport(ctx, singleMemoryUnit("source-b", "b", "key-b"))
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type attempt struct {
		result ImportResult
		err    error
	}
	results := make(chan attempt, 2)
	for _, candidate := range []struct {
		provider InterchangeProvider
		plan     ImportPlan
	}{{first, firstPlan}, {second, secondPlan}} {
		go func(provider InterchangeProvider, plan ImportPlan) {
			<-start
			result, err := provider.ApplyImport(ctx, plan)
			results <- attempt{result: result, err: err}
		}(candidate.provider, candidate.plan)
	}
	close(start)
	wins, stale := 0, 0
	for range 2 {
		attempt := <-results
		switch {
		case attempt.err == nil && attempt.result.Outcome == ImportApplied:
			wins++
		case errors.Is(attempt.err, ErrStaleDestination):
			stale++
		default:
			t.Fatalf("unexpected cross-instance result: %+v err=%v", attempt.result, attempt.err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("cross-instance outcomes: applied=%d stale=%d", wins, stale)
	}
	records, err := first.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("cross-instance race published %d records, want exactly one", len(records))
	}
}

func compactGraphUnit() InterchangeUnit {
	return InterchangeUnit{
		Declaration:     CanonicalDeclaration(),
		SourceProjectID: "fixture-source",
		Records: []Record{
			{
				ID: "policy", Kind: KindMemory, RevisionID: "policy-rev-current", Key: "policy",
				Title: "Policy", Body: "Current policy", Lifecycle: LifecycleActive,
				Author: "Ada <ada@example.test>", ChangeMessage: "Record policy", Origin: "native",
			},
			{
				ID: "guide", Kind: KindMemory, RevisionID: "guide-rev-current",
				Title: "Guide", Body: "Apply the policy", Lifecycle: LifecycleActive,
				Author: "Grace <grace@example.test>", AssistingAgent: "agent-b", ChangeMessage: "Record guide", Origin: "native",
				References: []Reference{
					{Local: true, BeadID: "policy", RevisionID: "policy-rev-current", ExpectedScope: "project", ExpectedKind: KindMemory},
					{ProjectID: "foreign-project", BeadID: "foreign-memory", RevisionID: "foreign-rev-9", ExpectedScope: "project", ExpectedKind: KindMemory},
				},
			},
			{
				ID: "archived", Kind: KindMemory, RevisionID: "archive-rev-current",
				Title: "Archived note", Body: "Historical note", Lifecycle: LifecycleArchived,
				Author: "Lin <lin@example.test>", ChangeMessage: "Archive note", Origin: "legacy_migration",
				References: []Reference{{
					Local: true, BeadID: "policy", RevisionID: "policy-rev-old",
					ExpectedScope: "project", ExpectedKind: KindMemory,
				}},
			},
			{
				ID: "task", Kind: KindTask, Title: "Implement policy", Body: "Read the linked policy",
				References: []Reference{{
					Local: true, BeadID: "policy", RevisionID: "policy-rev-current",
					ExpectedScope: "project", ExpectedKind: KindMemory,
				}},
			},
		},
	}
}

func singleMemoryUnit(source ProjectID, id BeadID, key string) InterchangeUnit {
	return InterchangeUnit{
		Declaration:     CanonicalDeclaration(),
		SourceProjectID: source,
		Records: []Record{{
			ID: id, Kind: KindMemory, RevisionID: RevisionID(id + "-revision"), Key: key,
			Title: string(id), Body: "body", Lifecycle: LifecycleActive,
			Author: "Fixture <fixture@example.test>", ChangeMessage: "Seed fixture", Origin: "native",
		}},
	}
}

func seedCompactGraph(t *testing.T, ctx context.Context, provider InterchangeProvider) {
	t.Helper()
	importUnit(t, ctx, provider, compactGraphUnit())
}

func importUnit(t *testing.T, ctx context.Context, provider InterchangeProvider, unit InterchangeUnit) ImportResult {
	t.Helper()
	plan, err := provider.PrepareImport(ctx, unit)
	if err != nil {
		t.Fatalf("PrepareImport: %v", err)
	}
	result, err := provider.ApplyImport(ctx, plan)
	if err != nil {
		t.Fatalf("ApplyImport: %v", err)
	}
	return result
}

func recordsByTitle(records []Record) map[string]Record {
	out := make(map[string]Record, len(records))
	for _, record := range records {
		out[record.Title] = record
	}
	return out
}

func assertMappedCurrent(t *testing.T, ref Reference, target Record) {
	t.Helper()
	if !ref.Local || ref.ProjectID != "" || ref.BeadID != target.ID || ref.RevisionID != target.RevisionID {
		t.Fatalf("current source-local locator was not remapped to new target: ref=%+v target=%+v", ref, target)
	}
}

func findReference(t *testing.T, refs []Reference, predicate func(Reference) bool) Reference {
	t.Helper()
	for _, ref := range refs {
		if predicate(ref) {
			return ref
		}
	}
	t.Fatalf("reference not found in %+v", refs)
	return Reference{}
}
