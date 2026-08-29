package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var mapLine = regexp.MustCompile(`^//\t([a-z]+\.go)\s`)

// Карта в доке пакета протухает на первом новом файле, если её никто
// не проверяет. Здесь проверяют в обе стороны.
func TestPackageMap(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := mapLine.FindStringSubmatch(line); m != nil {
			listed = append(listed, m[1])
		}
	}
	if len(listed) == 0 {
		t.Fatal("в доке пакета нет карты")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			actual = append(actual, n)
		}
	}

	for _, f := range listed {
		if !slices.Contains(actual, f) {
			t.Errorf("карта называет %s, которого нет", f)
		}
	}
	for _, f := range actual {
		if !slices.Contains(listed, f) {
			t.Errorf("%s есть в пакете, но не в карте", f)
		}
	}
}
