package issueops

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestEveryMutationFunctionJournals is the completeness guard for the durable
// mutations journal. It parses this package's source, builds the intra-package
// call graph, and asserts that every mutation entry point either records a
// journal row directly (calls one of the Record*InTx emit helpers) or calls a
// function that transitively does.
//
// This kills the enumeration-drift class that sank the decorator design: there,
// coverage was a hand-maintained list of overridden methods, and new mutation
// paths (ClaimReadyIssue, wisp ops, lease reclaim, cascade delete, …) silently
// slipped through. Here, if a listed mutation function stops emitting — directly
// or through its delegates — this test fails. When you add a NEW mutation entry
// point to issueops, add it to mutationEntryPoints below and make it emit.
func TestEveryMutationFunctionJournals(t *testing.T) {
	// The mutation entry points that must result in a journal row. Every write
	// plumbing bottoms out in one of these; see the emission sites for where
	// each records (some via a lowercase worker or a delegate, which the
	// transitive check below follows).
	mutationEntryPoints := []string{
		"CreateIssueInTx",
		"CreateIssueInTxWithResult",
		"CreateIssuesInTx",
		"CreateIssuesInTxWithResult",
		"PersistDependenciesWithOptionsResult", // creation-time dependency edges
		"UpdateIssueInTx",
		"UpdateIssueWithoutEventInTx",
		"CloseIssueInTx",
		"CloseIssueWithoutEventInTx",
		"ReopenIssueInTx",
		"DeleteIssueInTx",
		"DeleteIssuesInTx",
		"DeleteIssuesBySourceRepoInTx",
		"ClaimIssueInTx",
		"ClaimReadyIssueInTx",
		"UnclaimIssueInTx",
		"UnclaimIssueIfAssigneeInTx",
		"ReclaimExpiredLeasesInTx",
		"PromoteFromEphemeralInTx",
		"AddDependencyInTx",
		"RemoveDependencyInTx",
		"AddLabelInTx",
		"RemoveLabelInTx",
		"UpdateIssueIDInTx",
	}

	// The emit helpers a function calls to journal a row directly.
	emitHelpers := map[string]bool{
		"RecordMutationInTx":    true,
		"RecordDeleteInTx":      true,
		"RecordDepMutationInTx": true,
		"insertMutationRow":     true,
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse issueops package: %v", err)
	}

	// calls[fn] = set of intra-package function names fn calls (including the
	// emit helpers). directlyEmits[fn] = fn calls an emit helper.
	calls := map[string]map[string]bool{}
	directlyEmits := map[string]bool{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := fn.Name.Name
				callset := map[string]bool{}
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					// Intra-package calls are bare identifiers: foo(...).
					if ident, ok := call.Fun.(*ast.Ident); ok {
						callset[ident.Name] = true
						if emitHelpers[ident.Name] {
							directlyEmits[name] = true
						}
					}
					return true
				})
				calls[name] = callset
			}
		}
	}

	// Fixpoint: a function emits if it directly emits or calls a function that
	// emits.
	emits := map[string]bool{}
	for name := range directlyEmits {
		emits[name] = true
	}
	for changed := true; changed; {
		changed = false
		for name, callset := range calls {
			if emits[name] {
				continue
			}
			for callee := range callset {
				if emits[callee] {
					emits[name] = true
					changed = true
					break
				}
			}
		}
	}

	for _, entry := range mutationEntryPoints {
		if _, defined := calls[entry]; !defined {
			t.Errorf("mutation entry point %q not found in issueops — was it renamed? update mutationEntryPoints", entry)
			continue
		}
		if !emits[entry] {
			t.Errorf("mutation entry point %q does not journal: it neither calls a Record*InTx emit helper nor a function that transitively does", entry)
		}
	}
}
