// Command godoccheck reports exported top-level identifiers that lack a doc
// comment. Build-tag free; run: go run ./internal/tools/godoccheck .
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	bad := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && (info.Name() == "vendor" || strings.HasPrefix(info.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return nil
		}
		for _, d := range f.Decls {
			var names []string
			var doc *ast.CommentGroup
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Recv != nil {
					continue
				}
				names = []string{decl.Name.Name}
				doc = decl.Doc
			case *ast.GenDecl:

				for _, spec := range decl.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names = []string{s.Name.Name}
						doc = s.Doc
						if doc == nil && !decl.Lparen.IsValid() {
							doc = decl.Doc
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if ast.IsExported(n.Name) {
								names = append(names, n.Name)
							}
						}
						doc = s.Doc
						if doc == nil && len(decl.Specs) == 1 && !decl.Lparen.IsValid() {
							doc = decl.Doc
						}
					}
					checkNames(fset, names, doc, d, &bad)
					names = nil
				}
				continue
			}
			checkNames(fset, names, doc, d, &bad)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("checked: %d missing\n", bad)
	if bad > 0 {
		os.Exit(1)
	}
}

func checkNames(fset *token.FileSet, names []string, doc *ast.CommentGroup, d ast.Decl, bad *int) {
	for _, n := range names {
		if !ast.IsExported(n) {
			continue
		}
		pos := fset.Position(d.Pos())
		if doc == nil {
			fmt.Printf("%s:%d: %s lacks doc comment\n", pos.Filename, pos.Line, n)
			*bad++
			continue
		}

		text := strings.TrimSpace(strings.TrimPrefix(doc.List[0].Text, "//"))
		if first := strings.Fields(text); len(first) > 0 && first[0] != n {
			fmt.Printf("%s:%d: %s doc should begin with %q, got %q\n",
				pos.Filename, pos.Line, n, n, first[0])
			*bad++
		}
	}
}
