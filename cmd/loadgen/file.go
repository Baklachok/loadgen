// Прогон из файла: те же флаги, но в YAML — у прогона появляется место
// в репозитории, а не строка в истории шелла.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Ключ файла = имя флага: одна таблица имён на -h, README и файл, и applyFile
// не держит соответствия «поле → флаг». Односимвольные флаги в файле скупы,
// поэтому у них есть длинные синонимы — единственный новый словарь.
var longNames = map[string]string{
	"requests":    "n",
	"duration":    "z",
	"concurrency": "c",
	"method":      "m",
	"body":        "d",
	"timeout":     "t",
	"headers":     "H",
	"output":      "o",
}

// urlKey — единственный позиционный аргумент; в файле он такой же ключ.
const urlKey = "url"

// errFile помечает ошибку файла: её печатает run, тогда как ошибку Parse
// FlagSet уже напечатал сам вместе с подсказкой. Различаются они типом,
// а не префиксом строки.
type errFile struct{ error }

func (e errFile) Unwrap() error { return e.error }

// parseArgs — единственный путь от аргументов к разобранному FlagSet, общий
// для run и тестов: иначе тесты разбирают флаги в обход файла и зелены
// на сломанном коде. Сначала Parse, потом файл.
func parseArgs(fs *flag.FlagSet, f *flags, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *f.file == "" {
		return nil
	}

	url, err := applyFile(fs, *f.file)
	if err != nil {
		return errFile{err}
	}
	// url из файла — позиционный аргумент, и после Parse он ставится только
	// повторным Parse. Флаги при этом не сбрасываются: FlagSet хранит их
	// в Value, а Parse лишь заново раскладывает args.
	if url != "" && fs.NArg() == 0 {
		return fs.Parse([]string{url})
	}
	return nil
}

// applyFile ставит значения из файла через FlagSet.Set — после Parse.
//
// Загрузить их «в дефолты» нельзя: три правила в config() смотрят, задан ли
// флаг явно, а не на значение — конфликт -n/-z, подгонка -c, «-slo-error-rate 0»
// как «ни одной ошибки». Set делает значение заданным, fs.Visit его видит.
//
// Флаг, который человек назвал в строке, файл не трогает: Visit перечисляет
// ровно их. Первая версия применяла файл до Parse ради того же приоритета
// и держала три функции, угадывавшие по сырым аргументам то, что FlagSet
// после Parse знает точно; список bool-флагов в одной из них разошёлся
// с newFlags за день.
//
// Неизвестный ключ отвергается по Lookup: словаря допустимых полей нет.
func applyFile(fs *flag.FlagSet, path string) (url string, err error) {
	raw, err := os.ReadFile(path) //nolint:gosec // путь называет человек флагом -f
	if err != nil {
		return "", fmt.Errorf("-f: %w", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("-f %s: %w", path, err)
	}

	explicit := setFlags(fs)
	for key, value := range doc {
		if key == urlKey {
			url = fmt.Sprint(value)
			continue
		}
		name := key
		if long, ok := longNames[key]; ok {
			name = long
		}
		if fs.Lookup(name) == nil {
			return "", fmt.Errorf("-f %s: неизвестный ключ %q", path, key)
		}
		if explicit[name] {
			continue
		}
		if err := setEach(fs, name, value); err != nil {
			return "", fmt.Errorf("-f %s: %s: %w", path, key, err)
		}
	}
	return url, nil
}

// setEach — список ставится по элементу, как повторённый флаг (-H … -H …);
// скаляр — одной строкой. Все наши flag.Value разбирают строку.
func setEach(fs *flag.FlagSet, name string, value any) error {
	items, ok := value.([]any)
	if !ok {
		items = []any{value}
	}
	for _, it := range items {
		if err := fs.Set(name, fmt.Sprint(it)); err != nil {
			return err
		}
	}
	return nil
}

// isFileError — ошибка пришла из файла, а не из Parse.
func isFileError(err error) bool {
	var e errFile
	return errors.As(err, &e)
}
