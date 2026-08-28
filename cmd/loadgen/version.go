// Версия сборки: вшитая линкером при сборке через Makefile,
// иначе — та, что Go записал в бинарник сам.
package main

import (
	"fmt"
	"runtime/debug"
)

var version = "dev" // подставляется через -ldflags при сборке из Makefile

// repoURL — адрес репозитория в подписи. Константа, а не вывод из
// debug.ReadBuildInfo().Main.Path: в тестовом бинарнике этот путь другой,
// и две механики ради одной строки не окупаются.
const repoURL = "https://github.com/Baklachok/loadgen"

// userAgent — как инструмент представляется на той стороне. Подпись стоит
// всегда: админ, увидевший поток в логах, должен понять, что это и откуда.
// Без неё на провод уходит Go-http-client/1.1, по которому нагрузочный
// прогон неотличим от чего угодно другого.
func userAgent() string {
	return fmt.Sprintf("loadgen/%s (+%s)", buildVersion(), repoURL)
}

// buildVersion возвращает версию сборки. Приоритет у вшитого линкером значения,
// но если собирали не через Makefile — например, через `go install ...@latest` —
// подойдёт то, что Go записал в бинарник сам.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return versionFrom(info)
}

// versionFrom вынесена отдельно от чтения глобального состояния, чтобы её
// можно было проверить тестом.
func versionFrom(info *debug.BuildInfo) string {
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}

	// VCS-данные идут первыми: при сборке из рабочей копии Go кладёт в
	// Main.Version псевдоверсию вида v0.0.0-20260827120054-5f62ef91018b+dirty,
	// а короткий хеш рядом читается несравнимо лучше.
	if rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		return rev + dirty
	}

	// При `go install pkg@v1.2.3` VCS-данных нет, зато Main.Version — ровно v1.2.3.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return "dev"
}
