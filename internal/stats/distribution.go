package stats

// newDistribution — единственное место, где выбирается накопитель.
// HDR: память фиксирована (131 КБ) при любой длине прогона. Точная
// реализация samples остаётся рядом как эталон для тестов.
func newDistribution() distribution {
	return newHDR()
}
