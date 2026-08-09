package storage

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// contractLegRegistry is every backend leg the role contract lock holds to the
// whole role tier. A leg is in this table or it is not a leg.
//
// It is a REGISTRY rather than a directory scan because a scan gets both of the
// cases that matter exactly backwards. Auto-discovery blocks the leg nobody is
// wiring yet: a half-built backend package that imports the contract package to
// run its first three cases is discovered on the commit that creates it, and
// the lock demands the whole tier — hundreds of entrypoints wired, or hundreds
// of reasoned waivers — from a change that never meant to make that promise.
// And auto-discovery silently exempts the leg that most needs the lock: a leg
// living outside this tree is in no scan of it, so the backend with the least
// shared history with the other three is the one nothing checks.
//
// Registering is the deliberate act, and it is not free: a registered leg is
// held to every role contract entrypoint the conformance package exports, so it
// either wires the whole tier or carries a reasoned waiver per contract it
// cannot run (see unwiredContractEntrypoints). That cost is the point. It is
// paid by the change that decides the leg is a leg, not by the change that
// happens to add a directory.
//
// A wiringRoot may point anywhere in the repository, so a leg does not have to
// live under internal/storage to be locked.
//
// A distribution built on top of this repository registers its own legs from a
// file of its own — an init() calling registerContractLeg in a new _test.go
// file in this package — rather than editing this table, so its registration is
// its own commit and merges without conflict.
var contractLegRegistry []contractLeg

func init() {
	registerContractLeg(contractLeg{name: "dolt", wiringRoot: "internal/storage/dolt"})
	registerContractLeg(contractLeg{name: "embeddeddolt", wiringRoot: "internal/storage/embeddeddolt"})
	registerContractLeg(contractLeg{name: "uow", wiringRoot: "internal/storage/uow"})
}

// contractLeg is one registered backend leg: what the lock calls it, and where
// its contract wiring lives.
type contractLeg struct {
	// name is the leg's identity in every message the lock prints and the key
	// unwiredContractEntrypoints waives against.
	name string
	// wiringRoot is the directory holding the leg's *_test.go wiring,
	// slash-separated and relative to the repository root.
	wiringRoot string
}

// registerContractLeg adds a leg to the registry.
func registerContractLeg(leg contractLeg) {
	contractLegRegistry = append(contractLegRegistry, leg)
}

// storageTree is the subtree the tripwire watches: the one place an in-tree leg
// is most likely to appear without anyone registering it.
const storageTree = "internal/storage"

// registeredContractLegs returns the registry in a deterministic order, having
// checked that every entry is usable.
func registeredContractLegs(t *testing.T) []contractLeg {
	t.Helper()
	legs, err := validateContractLegRegistry(contractLegRegistry)
	if err != nil {
		t.Fatalf("the contract leg registry is unusable: %v", err)
	}
	if len(legs) == 0 {
		t.Fatal("no leg is registered; every lock over the registry would pass vacuously")
	}
	return legs
}

// validateContractLegRegistry sorts a registry by leg name and rejects the ways
// an entry can be unusable: a nameless leg, a leg with nowhere to look, a
// wiring root given as an absolute or escaping path, and two entries claiming
// one name — which would let one leg's waivers answer for the other.
//
// A wiring root that names nothing needs no check here. The lock resolves it,
// finds no test source, and reports the leg as naming none of the contract
// entrypoints, which is as loud as a failure gets.
func validateContractLegRegistry(registry []contractLeg) ([]contractLeg, error) {
	legs := slices.Clone(registry)
	slices.SortFunc(legs, func(a, b contractLeg) int { return strings.Compare(a.name, b.name) })
	seen := map[string]bool{}
	for _, leg := range legs {
		if strings.TrimSpace(leg.name) == "" {
			return nil, fmt.Errorf("a leg registered at %q has no name", leg.wiringRoot)
		}
		if seen[leg.name] {
			return nil, fmt.Errorf("leg %q is registered twice; one entry's waivers would answer for the other", leg.name)
		}
		seen[leg.name] = true
		if err := checkWiringRoot(leg.wiringRoot); err != nil {
			return nil, fmt.Errorf("leg %q: %w", leg.name, err)
		}
	}
	return legs, nil
}

// checkWiringRoot rejects a wiring root that is not a clean path inside the
// repository. Anything else resolves somewhere the lock cannot describe, and a
// leg the lock cannot describe is a leg nobody can act on when it fails.
func checkWiringRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("registered with no wiring root; the lock has nowhere to look")
	}
	if path.IsAbs(root) || filepath.IsAbs(root) || root != path.Clean(root) || strings.HasPrefix(root, "../") {
		return fmt.Errorf("wiring root %q is not a clean path relative to the repository root", root)
	}
	return nil
}

// unregisteredContractLegs reports the directories under root/storageTree whose
// tests import the conformance package and that no registered leg claims.
//
// This is the auto-discovery that used to enumerate the legs, kept and turned
// around. As an enumerator it decided what the lock covered, which is how a new
// directory could take on the whole tier by existing. As a tripwire it decides
// nothing: it only refuses to let a package in this tree import the contract
// package without saying, in the registry, that it is a leg and where its
// wiring lives.
func unregisteredContractLegs(root string, legs []contractLeg) ([]string, error) {
	claimed := map[string]bool{}
	for _, leg := range legs {
		claimed[path.Clean(leg.wiringRoot)] = true
	}
	var unregistered []string
	tree := filepath.Join(root, filepath.FromSlash(storageTree))
	err := filepath.WalkDir(tree, func(dir string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Name() == "testdata" && dir != tree {
			return fs.SkipDir
		}
		imports, err := importsContractPackage(dir)
		if err != nil || !imports {
			return err
		}
		relative, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		if slashed := filepath.ToSlash(relative); !claimed[slashed] {
			unregistered = append(unregistered, slashed)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(unregistered)
	return unregistered, nil
}

// importsContractPackage reports whether any test source in dir imports the
// conformance package. A package that only names it in a comment does not
// count: the test is the import, not the word.
func importsContractPackage(dir string) (bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return false, err
	}
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			return false, fmt.Errorf("parsing %s: %w", file, err)
		}
		if conformanceImportName(parsed) != "" {
			return true, nil
		}
	}
	return false, nil
}

// TestEveryContractImporterInTheStorageTreeIsRegistered fails when a package
// under internal/storage imports the conformance package without a registry
// entry.
func TestEveryContractImporterInTheStorageTreeIsRegistered(t *testing.T) {
	unregistered, err := unregisteredContractLegs(repositoryRoot(t), registeredContractLegs(t))
	if err != nil {
		t.Fatalf("scanning %s: %v", storageTree, err)
	}
	for _, dir := range unregistered {
		t.Errorf("%s imports the conformance package but is in no registry entry: register the leg "+
			"in contractLegRegistry with its wiring root — which commits it to the whole role contract "+
			"tier — or stop importing the contract package", dir)
	}
}

// TestTheTripwireCatchesAnUnregisteredLeg fabricates the case the registry
// exists to survive: a new backend directory in the storage tree that imports
// the contract package and that nobody registered.
//
// The tripwire is the only thing standing between that directory and silence,
// so it is proved against a tree built for the purpose rather than against the
// repository, where the answer is empty on every green day and an inert
// tripwire is indistinguishable from a clean one.
func TestTheTripwireCatchesAnUnregisteredLeg(t *testing.T) {
	root := fabricateRepository(t, map[string]string{
		"internal/storage/known/contract_test.go":      fabricatedWiring("known", "RunFabricatedContract"),
		"internal/storage/newcomer/contract_test.go":   fabricatedWiring("newcomer", "RunFabricatedContract"),
		"internal/storage/bystander/bystander_test.go": "package bystander\n",
	})
	legs := []contractLeg{{name: "known", wiringRoot: "internal/storage/known"}}

	unregistered, err := unregisteredContractLegs(root, legs)
	if err != nil {
		t.Fatalf("scanning the fabricated tree: %v", err)
	}
	if len(unregistered) != 1 || unregistered[0] != "internal/storage/newcomer" {
		t.Errorf("unregistered = %v, want just internal/storage/newcomer: the registered leg is "+
			"covered and the package that imports nothing is not a leg", unregistered)
	}
}

// TestARegisteredLegOutsideTheStorageTreeIsLockedToo proves the property the
// registry exists to make possible, and the one the directory scan could not
// express at all: a leg whose wiring lives somewhere else in the repository is
// held to the whole contract tier exactly like the three that live here.
//
// It fabricates a repository — a conformance package in miniature, a storage
// tree with nothing in it, and a leg off in another directory that wires one of
// the two contracts — and asserts both halves. The tripwire finds nothing,
// because the leg is not in the tree it watches, which is precisely the silence
// the old scan left behind. The lock finds the contract the leg skipped,
// because the leg is registered.
func TestARegisteredLegOutsideTheStorageTreeIsLockedToo(t *testing.T) {
	root := fabricateRepository(t, map[string]string{
		"backend/conformance/fabricated_contract.go":       fabricatedContractPackage,
		"internal/storage/placeholder/placeholder_test.go": "package placeholder\n",
		"internal/httpstore/client/leg_contract_test.go":   fabricatedWiring("client", "RunFabricatedContract"),
	})
	leg := contractLeg{name: "outsider", wiringRoot: "internal/httpstore/client"}
	if _, err := validateContractLegRegistry([]contractLeg{leg}); err != nil {
		t.Fatalf("a wiring root outside the storage tree was refused: %v", err)
	}

	unregistered, err := unregisteredContractLegs(root, nil)
	if err != nil {
		t.Fatalf("scanning the fabricated storage tree: %v", err)
	}
	if len(unregistered) != 0 {
		t.Errorf("the tripwire reported %v; a leg outside internal/storage is invisible to it, which is "+
			"why registration and not discovery is what puts a leg under the lock", unregistered)
	}

	entrypoints := roleContractEntrypoints(t, filepath.Join(root, "backend", "conformance"))
	if !slices.Equal(entrypoints, []string{"RunFabricatedContract", "RunSecondFabricatedContract"}) {
		t.Fatalf("fabricated contract tier = %v, want the two entrypoints that take a fixture", entrypoints)
	}
	dir := filepath.Join(root, filepath.FromSlash(leg.wiringRoot))

	wiring := inspectLegWiring(t, dir, entrypoints, nil)
	if wiring.named != 1 || !slices.Equal(wiring.missing, []string{"RunSecondFabricatedContract"}) {
		t.Errorf("wiring = %+v, want the one contract it names counted and the one it skips reported; "+
			"a leg outside internal/storage has to be as answerable as one inside it", wiring)
	}

	waived := map[string]string{"RunSecondFabricatedContract": "the fabricated leg cannot run it"}
	if waivedWiring := inspectLegWiring(t, dir, entrypoints, waived); len(waivedWiring.missing) != 0 {
		t.Errorf("waived wiring still reports %v as missing; the per-leg waiver has to spend the same "+
			"way for a leg registered from outside the tree", waivedWiring.missing)
	}
	spent := map[string]string{"RunFabricatedContract": "claims the leg cannot run one it does"}
	if wiredWiring := inspectLegWiring(t, dir, entrypoints, spent); !slices.Equal(wiredWiring.falselyWaived, []string{"RunFabricatedContract"}) {
		t.Errorf("falselyWaived = %v, want the contract the leg actually runs; shrink-only is what keeps "+
			"a waiver list from outliving its reason", wiredWiring.falselyWaived)
	}
}

// TestTheRegistryRefusesAnEntryTheLockCannotActOn covers the entries that
// would make a failure unactionable or, worse, quiet: a nameless leg, a leg
// with nowhere to look, a root that escapes the repository, and two entries
// claiming one name — which would hand one leg's waivers to the other.
func TestTheRegistryRefusesAnEntryTheLockCannotActOn(t *testing.T) {
	tests := []struct {
		name     string
		registry []contractLeg
		want     string
	}{
		{
			name:     "no name",
			registry: []contractLeg{{wiringRoot: "internal/storage/dolt"}},
			want:     "has no name",
		},
		{
			name:     "no wiring root",
			registry: []contractLeg{{name: "dolt"}},
			want:     "no wiring root",
		},
		{
			name:     "absolute wiring root",
			registry: []contractLeg{{name: "dolt", wiringRoot: "/etc/dolt"}},
			want:     "not a clean path",
		},
		{
			name:     "escaping wiring root",
			registry: []contractLeg{{name: "dolt", wiringRoot: "../elsewhere"}},
			want:     "not a clean path",
		},
		{
			name: "one name twice",
			registry: []contractLeg{
				{name: "dolt", wiringRoot: "internal/storage/dolt"},
				{name: "dolt", wiringRoot: "internal/storage/other"},
			},
			want: "registered twice",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateContractLegRegistry(test.registry)
			if err == nil {
				t.Fatalf("registry %+v was accepted", test.registry)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %v, want it to say %q", err, test.want)
			}
		})
	}
}

// fabricatedContractPackage is a conformance package in miniature: a fixture
// type, two role-tier entrypoints that take it, and a RunAll that does not —
// the same shape roleContractEntrypoints reads in the real one.
const fabricatedContractPackage = `package conformance

import "testing"

// FabricatedFixture stands in for a role fixture.
type FabricatedFixture struct{}

// RunFabricatedContract is role tier: its last parameter is a fixture.
func RunFabricatedContract(t *testing.T, fixture *FabricatedFixture) {}

// RunSecondFabricatedContract is the contract the fabricated leg skips.
func RunSecondFabricatedContract(t *testing.T, fixture *FabricatedFixture) {}

// RunAll takes no fixture, so it is not role tier and not in the census.
func RunAll(t *testing.T) {}
`

// fabricatedWiring returns test source for package name that imports the
// conformance package under its own name and calls the given entrypoints.
func fabricatedWiring(name string, entrypoints ...string) string {
	source := "package " + name + "\n\nimport (\n\t\"testing\"\n\n\t\"" + conformancePackage + "\"\n)\n\n" +
		"func TestFabricated(t *testing.T) {\n"
	for _, entrypoint := range entrypoints {
		source += "\tconformance." + entrypoint + "(t, nil)\n"
	}
	return source + "}\n"
}

// fabricateRepository writes files — keyed by slash-separated path relative to
// the root — into a temporary directory and returns that root.
func fabricateRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, source := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("fabricating %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
			t.Fatalf("fabricating %s: %v", path, err)
		}
	}
	return root
}
