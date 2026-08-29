package stats

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"testing"
	"time"
)

// Шов держится на одном правиле: Accumulator не лезет в сырые замеры.
// Раньше он доставал у накопителя отсортированный слайс и строил гистограмму
// сам — у HDR слайса нет, и такая строка расползлась бы при замене.
// Комментарий об этом протухнет, тест — нет.
func TestAccumulatorTouchesNoRawSamples(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "accumulator.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "sorted" {
			t.Errorf("%s: Accumulator зовёт sorted() — шов протёк, гистограмму обязан считать сам накопитель",
				fset.Position(call.Pos()))
		}
		return true
	})
}

// Обещание из README закреплено числом. Лежит у шва, потому что это тест
// не на hdr, а на то, что две его реализации взаимозаменяемы: на реалистичных задержках HDR
// с тремя значащими цифрами расходится с ближайшим рангом меньше чем
// на 0.1%. Тест не на код, а на утверждение, которое человек прочтёт.
func TestHDRAccuracy(t *testing.T) {
	const n = 100_000
	r := rand.New(rand.NewSource(1))

	exact, approx := &samples{}, newHDR()
	for i := 0; i < n; i++ {
		// лог-нормальная форма: средняя ~2 мс, хвост до сотен
		v := time.Duration(500_000 * (1 + r.ExpFloat64()*3))
		exact.add(v)
		approx.add(v)
	}
	e, a := exact.latencies(), approx.latencies()

	for _, tt := range []struct {
		name          string
		exact, approx time.Duration
	}{
		{"p50", e.P50, a.P50}, {"p90", e.P90, a.P90}, {"p95", e.P95, a.P95}, {"p99", e.P99, a.P99},
	} {
		diff := float64(tt.approx-tt.exact) / float64(tt.exact) * 100
		if diff < 0 {
			diff = -diff
		}
		if diff > hdrTolerance*100 {
			t.Errorf("%s: точн=%v hdr=%v, расхождение %.3f%% при обещанных %.1f%%", tt.name, tt.exact, tt.approx, diff, hdrTolerance*100)
		}
	}
	if e.Min != a.Min || e.Max != a.Max {
		t.Errorf("min/max обязаны быть точными: точн=%v/%v hdr=%v/%v", e.Min, e.Max, a.Min, a.Max)
	}
}

// Каркас гистограммы один — bucketize. Раньше он был выписан дважды,
// в samples и в hdr, и отличался одним циклом; расхождение при правке шкалы
// никто бы не заметил, потому что оба теста зелёные каждый по себе.
func TestHistogramScaffoldIsShared(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"latency.go", "hdr.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "histogram" {
				return true
			}
			// Тело histogram — только вызов bucketize, никакой своей шкалы.
			if hasIdent(fn.Body, "width") {
				t.Errorf("%s: histogram строит шкалу сама вместо bucketize", fset.Position(fn.Pos()))
			}
			return false
		})
	}
}

func hasIdent(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if id, ok := x.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}
