package report

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Карта в доке пакета — единственное место, где виден порядок секций:
// имена файлов его выразить не могут. Комментарий, который некому проверить,
// расходится с кодом на первой же правке, поэтому его проверяет тест.
func TestPackageMap(t *testing.T) {
	listed := filesInMap(t)
	declared := filesDeclaringSections(t)

	t.Run("каждый названный файл существует", func(t *testing.T) {
		for _, f := range listed {
			if _, err := os.Stat(f); err != nil {
				t.Errorf("карта называет %s, которого нет", f)
			}
		}
	})

	// Главное: добавили секцию в sections — обязаны назвать её в карте.
	t.Run("секции карты и sections совпадают", func(t *testing.T) {
		for _, f := range declared {
			if !slices.Contains(listed, f) {
				t.Errorf("%s объявляет секцию, но в карте его нет", f)
			}
		}
	})
}

var mapLine = regexp.MustCompile(`^//\t([a-z]+\.go)\s`)

// filesInMap собирает имена файлов из дока пакета. Док читается из исходника,
// а не из doc.Package: нам нужны ровно те строки, что видит человек.
func filesInMap(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile("report.go")
	if err != nil {
		t.Fatal(err)
	}

	var files []string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := mapLine.FindStringSubmatch(line); m != nil {
			files = append(files, m[1])
		}
	}
	if len(files) == 0 {
		t.Fatal("в доке пакета не нашлось ни одной строки карты")
	}
	return files
}

// filesDeclaringSections находит файлы, где объявлены функции из списка
// sections. Через AST, а не поиском по тексту: список — единственный
// источник правды о том, что печатается.
//
// Файлы разбираются поштучно: parser.ParseDir и ast.Package объявлены
// устаревшими, а глушить предупреждение о том, что однажды сломается,
// значит оставить поломку следующему.
func filesDeclaringSections(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	names := sectionNames(t, parse(t, fset, "text.go"))
	if len(names) == 0 {
		t.Fatal("список sections не найден или пуст")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	var files []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if declaresAny(parse(t, fset, name), names) {
			files = append(files, name)
		}
	}
	return files
}

func parse(t *testing.T, fset *token.FileSet, name string) *ast.File {
	t.Helper()

	f, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func declaresAny(file *ast.File, names []string) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && slices.Contains(names, fn.Name.Name) {
			return true
		}
	}
	return false
}

// sectionNames читает имена из объявления var sections.
func sectionNames(t *testing.T, file *ast.File) []string {
	t.Helper()

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "sections" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, el := range lit.Elts {
				if id, ok := el.(*ast.Ident); ok {
					names = append(names, id.Name)
				}
			}
		}
		return false
	})
	return names
}
