package provider

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// llm's exported identifiers are listed through go/types, type-checked from
// source by go/importer, which needs no network and no module beyond the one
// already in the build. A name llm exports that the shim does not declare
// would compile today and break the day an importer needs it.
func TestTheShimDeclaresEveryExportOfLlm(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := importer.ForCompiler(fset, "source", nil).Import("github.com/rfizzle/llm")
	if err != nil {
		t.Fatalf("importing llm: %v", err)
	}
	file, err := parser.ParseFile(fset, "shim.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing the shim: %v", err)
	}
	declared := map[string]bool{}
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				declared[s.Name.Name] = true
			case *ast.ValueSpec:
				for _, n := range s.Names {
					declared[n.Name] = true
				}
			}
		}
	}
	var missing []string
	exported := 0
	for _, name := range pkg.Scope().Names() {
		if !pkg.Scope().Lookup(name).Exported() {
			continue
		}
		exported++
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	if exported == 0 {
		t.Fatal("llm lists no exported identifiers; the importer found nothing")
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("llm exports %s and the shim does not declare it", name)
	}
	for name := range declared {
		if obj := pkg.Scope().Lookup(name); obj == nil || !obj.Exported() {
			t.Errorf("the shim declares %s and llm does not export it", name)
		}
	}
}
