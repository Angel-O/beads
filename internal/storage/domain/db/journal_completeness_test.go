package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestEveryRepositoryMutatorJournals is the completeness guard for the
// unit-of-work write plumbing. Unlike the DoltStorage plumbing, several of this
// package's repository mutators reimplement their own SQL instead of routing
// through issueops, so the issueops seam alone does NOT cover them. This test
// enumerates every mutator method on the issue/dependency/label repositories and
// asserts each one journals — either by calling an issueops.Record*InTx emit
// helper directly, or by delegating to an issueops function that emits
// (CloseIssueInTx / ReopenIssueInTx / ClaimReadyIssueInTx /
// ReclaimExpiredLeasesInTx), or by delegating to a sibling repository method
// that does.
//
// A new reimplemented mutator that forgets to journal fails this test.
func TestEveryRepositoryMutatorJournals(t *testing.T) {
	// Repository receiver types whose mutator methods must journal.
	mutatorReceivers := map[string]bool{
		"issueSQLRepositoryImpl":      true,
		"dependencySQLRepositoryImpl": true,
		"labelSQLRepositoryImpl":      true,
	}

	// A method name is a mutator when it starts with one of these verbs.
	mutatorVerb := regexp.MustCompile(`^(Insert|Update|Delete|Claim|Close|Reopen|Unclaim|Reclaim|Promote|Reparent|Rename|Add|Remove|Set)`)

	// Methods that are legitimately not journalled: bulk cascade cleanups run
	// while their parent issue delete (already journalled) removes the rows.
	allowNoJournal := map[string]bool{
		"DeleteAllForIDs": true,
	}

	// Calls that count as journalling coverage: the direct emit helpers, the
	// issueops functions that emit internally, and delegation to a sibling repo
	// method (matched loosely by the "Insert" method name for InsertBatch).
	coverageCalls := map[string]bool{
		"RecordMutationInTx":       true,
		"RecordDeleteInTx":         true,
		"RecordDepMutationInTx":    true,
		"CloseIssueInTx":           true,
		"ReopenIssueInTx":          true,
		"ClaimReadyIssueInTx":      true,
		"ReclaimExpiredLeasesInTx": true,
		"Insert":                   true, // InsertBatch delegates to Insert
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse domain/db package: %v", err)
	}

	var checked int
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
					continue
				}
				recv := receiverTypeName(fn.Recv.List[0].Type)
				if !mutatorReceivers[recv] {
					continue
				}
				name := fn.Name.Name
				if !mutatorVerb.MatchString(name) || allowNoJournal[name] {
					continue
				}

				covered := false
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && coverageCalls[sel.Sel.Name] {
						covered = true
					}
					return true
				})
				checked++
				if !covered {
					t.Errorf("%s.%s is a mutator that does not journal: it neither calls an issueops.Record*InTx emit helper, nor delegates to an emitting issueops function, nor to a sibling mutator that journals", recv, name)
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("guard found no mutator methods — receiver names or parsing changed; the completeness guard is not actually running")
	}
}

// receiverTypeName returns the bare type name of a method receiver
// (e.g. *issueSQLRepositoryImpl -> issueSQLRepositoryImpl).
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}
