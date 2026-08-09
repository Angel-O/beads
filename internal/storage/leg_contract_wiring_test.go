package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// conformancePackage is the import path whose Run entrypoints each leg wires.
const conformancePackage = "github.com/steveyegge/beads/backend/conformance"

// contractLegs are the backends that must answer the whole role tier. All three
// wire every entrypoint today; this test is what stops that from decaying one
// forgotten line at a time.
var contractLegs = []string{"dolt", "embeddeddolt", "uow"}

// TestEveryLegWiresEveryRoleContract fails when a backend leg skips a role
// contract the conformance package exports.
//
// The contracts are shared source: writing one is worth nothing until every leg
// runs it, and nothing about adding a Run entrypoint reminds an author to wire
// it into three test files. A leg missing one is invisible — its own suite still
// passes, and the entrypoint still has two other backends behind it, so the
// silence looks exactly like coverage.
//
// It reads source rather than running anything, so it holds for the legs whose
// suites need infrastructure this test does not have: the server-backed store
// needs a live sql-server and the embedded store needs cgo, and their wiring is
// checked here either way.
func TestEveryLegWiresEveryRoleContract(t *testing.T) {
	root := repositoryRoot(t)
	entrypoints := roleContractEntrypoints(t, filepath.Join(root, "backend", "conformance"))
	if len(entrypoints) == 0 {
		t.Fatal("the conformance package exports no role contract entrypoints; this test would pass vacuously")
	}

	for _, leg := range contractLegs {
		t.Run(leg, func(t *testing.T) {
			wired := conformanceEntrypointsWiredBy(t, filepath.Join(root, "internal", "storage", leg))
			var missing []string
			for _, name := range entrypoints {
				if !wired[name] {
					missing = append(missing, name)
				}
			}
			if len(missing) == 0 {
				return
			}
			t.Errorf("%s runs %d of the %d role contract entrypoints; it never names: %s",
				leg, len(entrypoints)-len(missing), len(entrypoints), strings.Join(missing, ", "))
		})
	}
}

// repositoryRoot locates the module root from this file's own path.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// roleContractEntrypoints reports the exported Run functions the conformance
// package's *_contract.go files declare, which is the role tier: RunAll, the
// audit suites and the other portable entrypoints live in files of their own
// and are wired once per leg rather than case by case.
func roleContractEntrypoints(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), "_contract.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Run") || !ast.IsExported(fn.Name.Name) {
					continue
				}
				names = append(names, fn.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// conformanceEntrypointsWiredBy reports the conformance entrypoints a leg's
// test sources name, resolved through the file's own import of the conformance
// package so a renamed import still counts.
//
// It counts every reference rather than only calls, because a leg is free to
// wire an entrypoint as a value: the unit-of-work leg drives five of its roles
// from a table whose rows hold `run: conformance.RunX` and call it later
// through the field. Counting calls alone read those ninety-one contracts as
// unwired.
func conformanceEntrypointsWiredBy(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	wired := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			local := conformanceImportName(file)
			if local == "" {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				selector, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkgName, ok := selector.X.(*ast.Ident); ok && pkgName.Name == local {
					wired[selector.Sel.Name] = true
				}
				return true
			})
		}
	}
	return wired
}

// conformanceImportName reports the name a file refers to the conformance
// package by, or "" when it does not import it.
func conformanceImportName(file *ast.File) string {
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if path != conformancePackage {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return path[strings.LastIndexByte(path, '/')+1:]
	}
	return ""
}
