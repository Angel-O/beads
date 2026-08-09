package conformance

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/issueops"
	"github.com/steveyegge/beads/memoryops"
)

// roleMethod names one method of one role interface on the public facade.
type roleMethod struct {
	// Role is the qualified interface name, e.g. "issueops.Reader".
	Role string
	// Method is the method name, e.g. "Ready".
	Method string
}

// String renders the fully qualified name the gate reports and waives by.
func (rm roleMethod) String() string { return rm.Role + "." + rm.Method }

// facadePackages maps each facade package's import path to the qualifier this
// gate names its interfaces by. The paths come from real types rather than
// string literals, so moving a package breaks the build here instead of
// quietly emptying the census.
var facadePackages = map[string]string{
	reflect.TypeOf((*issueops.Reader)(nil)).Elem().PkgPath():    "issueops",
	reflect.TypeOf((*memoryops.Memories)(nil)).Elem().PkgPath(): "memoryops",
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// repoRoot locates the module root from this file's own path, so the gate
// finds the facade packages without depending on the working directory.
func repoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", ".."), nil
}

// parseFacadeInterfaces reads one facade package's non-test sources and reports
// every exported interface it declares, keyed "qualifier.Interface", with its
// method names.
//
// Source is the census's authority rather than reflection because reflection
// cannot enumerate a package's types: it can only answer questions about types
// something already names, and naming them is the hand-written list this gate
// exists to abolish. An interface the package declares but nothing else in the
// module mentions — issueops.Importer is one today — is invisible to any
// reflection-only census and visible here.
//
// An embedded interface is refused rather than skipped: silently counting zero
// methods for it would be the same aspirational coverage the gate is here to
// end. Resolving embeds is a change to make when the facade grows one.
func parseFacadeInterfaces(dir, qualifier string) (map[string][]string, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", dir, err)
	}
	roles := map[string][]string{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Assign.IsValid() || !ast.IsExported(ts.Name.Name) {
						continue
					}
					it, ok := ts.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					name := qualifier + "." + ts.Name.Name
					methods := []string{}
					for _, field := range it.Methods.List {
						if len(field.Names) == 0 {
							return nil, fmt.Errorf("%s embeds an interface; this census cannot resolve embedded method sets", name)
						}
						for _, m := range field.Names {
							methods = append(methods, m.Name)
						}
					}
					sort.Strings(methods)
					roles[name] = methods
				}
			}
		}
	}
	return roles, nil
}

// roleFacade is the whole public role surface: every exported interface the
// facade packages declare, with its method set.
func roleFacade() (map[string][]string, error) {
	root, err := repoRoot()
	if err != nil {
		return nil, err
	}
	facade := map[string][]string{}
	for path, qualifier := range facadePackages {
		dir := filepath.Join(root, filepath.Base(path))
		roles, err := parseFacadeInterfaces(dir, qualifier)
		if err != nil {
			return nil, err
		}
		for name, methods := range roles {
			facade[name] = methods
		}
	}
	return facade, nil
}

// reflectRoleAccessors reports the facade roles a backend hands out through a
// storage accessor — a method taking nothing and returning (role, error) — with
// method sets taken from the compiler rather than from source. It is the
// independent second opinion the source census is checked against; it cannot
// stand alone, because a role with no accessor never appears in it.
func reflectRoleAccessors() map[string][]string {
	surface := reflect.TypeOf((*storage.DoltStorage)(nil)).Elem()
	roles := map[string][]string{}
	for i := range surface.NumMethod() {
		signature := surface.Method(i).Type
		if signature.NumIn() != 0 || signature.NumOut() != 2 || signature.Out(1) != errorType {
			continue
		}
		role := signature.Out(0)
		if role.Kind() != reflect.Interface {
			continue
		}
		qualifier, ok := facadePackages[role.PkgPath()]
		if !ok {
			continue
		}
		methods := make([]string, 0, role.NumMethod())
		for j := range role.NumMethod() {
			methods = append(methods, role.Method(j).Name)
		}
		sort.Strings(methods)
		roles[qualifier+"."+role.Name()] = methods
	}
	return roles
}

// scanRoleCalls reports which role methods the conformance sources at dir
// actually call, mapped to the functions that call them.
//
// It resolves a call's receiver rather than matching method names, so a
// role method named Close is told apart from every other Close in the package.
// A receiver resolves when it is a role-typed parameter, local variable or
// assignment, or a field of a struct declared in the package whose own type is
// a role interface — which is the shape every contract fixture uses.
//
// Only functions reachable from an exported Run entrypoint count, so a contract
// case cannot be credited to a helper nothing runs. Methods with receivers are
// treated as reachable: resolving which values reach them needs type flow this
// scan does not do, and crediting a live helper is the safer error.
func scanRoleCalls(dir string) (map[roleMethod][]string, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", dir, err)
	}

	covered := map[roleMethod]map[string]bool{}
	for _, pkg := range pkgs {
		fields := roleTypedFields(pkg)
		reachable := reachableFuncs(pkg)
		for _, file := range pkg.Files {
			imports := importPaths(file)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				name := funcKey(fn)
				if !reachable[name] {
					continue
				}
				for target := range roleCallsIn(fn, imports, fields) {
					if covered[target] == nil {
						covered[target] = map[string]bool{}
					}
					covered[target][name] = true
				}
			}
		}
	}

	out := map[roleMethod][]string{}
	for target, callers := range covered {
		names := make([]string, 0, len(callers))
		for name := range callers {
			names = append(names, name)
		}
		sort.Strings(names)
		out[target] = names
	}
	return out, nil
}

// roleTypedFields maps each struct type in the package to its role-typed
// fields, so `fixture.Closer.CloseBatch(...)` resolves to the role the fixture
// declares rather than to whatever another fixture calls its Closer.
func roleTypedFields(pkg *ast.Package) map[string]map[string]string {
	fields := map[string]map[string]string{}
	for _, file := range pkg.Files {
		imports := importPaths(file)
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					role := roleTypeName(field.Type, imports)
					if role == "" {
						continue
					}
					if fields[ts.Name.Name] == nil {
						fields[ts.Name.Name] = map[string]string{}
					}
					for _, name := range field.Names {
						fields[ts.Name.Name][name.Name] = role
					}
				}
			}
		}
	}
	return fields
}

// reachableFuncs reports the package functions reachable from an exported Run
// entrypoint, walking calls to package-level function names.
func reachableFuncs(pkg *ast.Package) map[string]bool {
	declared := map[string]bool{}
	calls := map[string][]string{}
	var queue []string
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := funcKey(fn)
			declared[name] = true
			if fn.Recv != nil || strings.HasPrefix(fn.Name.Name, "Run") && ast.IsExported(fn.Name.Name) {
				queue = append(queue, name)
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if callee, ok := call.Fun.(*ast.Ident); ok {
					calls[name] = append(calls[name], callee.Name)
				}
				return true
			})
		}
	}
	reachable := map[string]bool{}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if reachable[name] || !declared[name] {
			continue
		}
		reachable[name] = true
		queue = append(queue, calls[name]...)
	}
	return reachable
}

// roleCallsIn reports the role methods one function calls.
func roleCallsIn(fn *ast.FuncDecl, imports map[string]string, fields map[string]map[string]string) map[roleMethod]bool {
	roleVars := map[string]string{}
	structVars := map[string]string{}
	bind := func(names []*ast.Ident, typ ast.Expr) {
		if role := roleTypeName(typ, imports); role != "" {
			for _, name := range names {
				roleVars[name.Name] = role
			}
			return
		}
		if named := localTypeName(typ); named != "" {
			for _, name := range names {
				structVars[name.Name] = named
			}
		}
	}
	bindParams := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, param := range list.List {
			bind(param.Names, param.Type)
		}
	}
	bindParams(fn.Recv)
	bindParams(fn.Type.Params)

	resolve := func(expr ast.Expr) string {
		switch e := expr.(type) {
		case *ast.Ident:
			return roleVars[e.Name]
		case *ast.SelectorExpr:
			if base, ok := e.X.(*ast.Ident); ok {
				return fields[structVars[base.Name]][e.Sel.Name]
			}
		}
		return ""
	}

	// Bindings first, so a call site reached before its variable's declaration
	// in traversal order still resolves.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			bindParams(node.Type.Params)
		case *ast.ValueSpec:
			bind(node.Names, node.Type)
		case *ast.AssignStmt:
			if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			target, ok := node.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if role := resolve(node.Rhs[0]); role != "" {
				roleVars[target.Name] = role
			}
		}
		return true
	})

	found := map[roleMethod]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if role := resolve(selector.X); role != "" {
			found[roleMethod{Role: role, Method: selector.Sel.Name}] = true
		}
		return true
	})
	return found
}

// funcKey names a declaration for the call graph: bare for a function, and
// receiver-qualified for a method.
func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return localTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

// importPaths maps each file-local package name to the path it imports.
func importPaths(file *ast.File) map[string]string {
	paths := map[string]string{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		name := path[strings.LastIndexByte(path, '/')+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		paths[name] = path
	}
	return paths
}

// roleTypeName reports the qualified role name a type expression denotes, or
// "" when it names something outside the facade packages.
func roleTypeName(typ ast.Expr, imports map[string]string) string {
	selector, ok := typ.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	qualifier, ok := facadePackages[imports[pkg.Name]]
	if !ok {
		return ""
	}
	return qualifier + "." + selector.Sel.Name
}

// localTypeName reports the package-local type name a type expression denotes,
// dropping one level of pointer.
func localTypeName(typ ast.Expr) string {
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// The scanner's own cases. Each writes a miniature conformance package to a
// temporary directory and asserts what the scan makes of it.

func TestScanRoleCallsResolvesFixtureFieldsHelpersAndAliases(t *testing.T) {
	dir := writeFakeContractPackage(t, `package fake

import (
	"context"

	publicops "github.com/steveyegge/beads/issueops"
)

type ReaderFixture struct {
	Reader publicops.Reader
}

type ClaimerFixture struct {
	// Two fixtures naming one field differently typed: resolving by field
	// name alone would credit the wrong role.
	Claimer publicops.ReadyClaimer
}

func RunReaderGetsAnIssue(ctx context.Context, fixture ReaderFixture) {
	fixture.Reader.Get(ctx, "id")
	readerList(ctx, fixture)
}

func RunReaderReadyThroughAnAlias(ctx context.Context, fixture ReaderFixture) {
	reader := fixture.Reader
	reader.Ready(ctx)
}

func RunClaimerClaimsTheFront(ctx context.Context, fixture ClaimerFixture) {
	fixture.Claimer.ClaimNext(ctx)
}

func readerList(ctx context.Context, fixture ReaderFixture) {
	fixture.Reader.List(ctx)
}
`)
	covered, err := scanRoleCalls(dir)
	if err != nil {
		t.Fatalf("scanning the fabricated package: %v", err)
	}
	for _, want := range []roleMethod{
		{Role: "issueops.Reader", Method: "Get"},
		{Role: "issueops.Reader", Method: "Ready"},
		{Role: "issueops.Reader", Method: "List"},
		{Role: "issueops.ReadyClaimer", Method: "ClaimNext"},
	} {
		if len(covered[want]) == 0 {
			t.Errorf("%s was not seen as covered; scan found %v", want, covered)
		}
	}
	if callers := covered[roleMethod{Role: "issueops.Claimer", Method: "ClaimNext"}]; len(callers) != 0 {
		t.Errorf("ClaimNext was credited to issueops.Claimer (%v); the field is typed ReadyClaimer", callers)
	}
	if callers := covered[roleMethod{Role: "issueops.Reader", Method: "List"}]; len(callers) != 1 || callers[0] != "readerList" {
		t.Errorf("List was credited to %v, want the helper that calls it", callers)
	}
}

func TestScanRoleCallsIgnoresAHelperNoEntrypointRuns(t *testing.T) {
	dir := writeFakeContractPackage(t, `package fake

import (
	"context"

	publicops "github.com/steveyegge/beads/issueops"
)

type ReaderFixture struct {
	Reader publicops.Reader
}

func RunReaderGetsAnIssue(ctx context.Context, fixture ReaderFixture) {
	fixture.Reader.Get(ctx, "id")
}

func orphanedHelper(ctx context.Context, fixture ReaderFixture) {
	fixture.Reader.Ready(ctx)
}
`)
	covered, err := scanRoleCalls(dir)
	if err != nil {
		t.Fatalf("scanning the fabricated package: %v", err)
	}
	if callers := covered[roleMethod{Role: "issueops.Reader", Method: "Ready"}]; len(callers) != 0 {
		t.Errorf("Ready was credited to %v, but only an unreachable helper calls it", callers)
	}
	if len(covered[roleMethod{Role: "issueops.Reader", Method: "Get"}]) == 0 {
		t.Error("Get was not credited to the entrypoint that calls it")
	}
}

func TestScanRoleCallsTellsRoleMethodsApartFromLookalikes(t *testing.T) {
	dir := writeFakeContractPackage(t, `package fake

import (
	"context"

	publicops "github.com/steveyegge/beads/issueops"
)

type LifecycleFixture struct {
	Lifecycle publicops.Lifecycle
	Rows      *rows
}

type rows struct{}

func (r *rows) Close() {}

func RunLifecycleCreates(ctx context.Context, fixture LifecycleFixture) {
	fixture.Lifecycle.Create(ctx, publicops.CreateRequest{})
	fixture.Rows.Close()
}
`)
	covered, err := scanRoleCalls(dir)
	if err != nil {
		t.Fatalf("scanning the fabricated package: %v", err)
	}
	if len(covered[roleMethod{Role: "issueops.Lifecycle", Method: "Create"}]) == 0 {
		t.Error("Create was not credited to the entrypoint that calls it")
	}
	if callers := covered[roleMethod{Role: "issueops.Lifecycle", Method: "Close"}]; len(callers) != 0 {
		t.Errorf("Lifecycle.Close was credited to %v, but only *rows.Close is called", callers)
	}
}

func TestParseFacadeInterfacesRefusesAnEmbeddedInterface(t *testing.T) {
	dir := writeFakeContractPackage(t, `package fake

type Reader interface {
	Get()
}

type Wider interface {
	Reader
	List()
}
`)
	if _, err := parseFacadeInterfaces(dir, "fake"); err == nil {
		t.Error("parsing an embedded interface succeeded; the census would silently count zero methods for it")
	}
}

// writeFakeContractPackage writes source to a temporary directory as a contract
// file and returns the directory.
func writeFakeContractPackage(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fake_contract.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("writing the fabricated package: %v", err)
	}
	return dir
}
