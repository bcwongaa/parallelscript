// Word frequency, the twin of main.pts: the same text, made with float64 arithmetic as JS
// makes it, split with strings.Split and counted in a map[string]int (one lookup per word,
// where a JS Map takes a get and a set), sorted with sort.Slice.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

var syllables = []string{"ka", "lo", "mi", "ne", "ru", "so", "ta", "vi", "ze", "po", "qua"}

func word(i float64) string {
	w := syllables[int(math.Mod(i, 11))]
	for rest := math.Floor(i / 11); rest > 0; rest = math.Floor(rest / 11) {
		w += syllables[int(math.Mod(rest, 11))]
	}
	return w
}

func text(words int, vocabulary float64) string {
	parts := []string{}
	seed := 42.0
	for i := 0; i < words; i++ {
		seed = math.Mod(seed*48271, 2147483647)
		r := seed / 2147483647
		parts = append(parts, word(math.Floor(r*r*vocabulary)))
	}
	return strings.Join(parts, " ")
}

func main() {
	counts := map[string]int{}
	for _, w := range strings.Split(text(2000000, 20000), " ") {
		counts[w]++
	}
	type entry struct {
		word  string
		count int
	}
	sorted := make([]entry, 0, len(counts))
	for w, n := range counts {
		sorted = append(sorted, entry{w, n})
	}
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].count != sorted[b].count {
			return sorted[a].count > sorted[b].count
		}
		return sorted[a].word < sorted[b].word
	})
	fmt.Println(len(counts))
	for _, e := range sorted[:10] {
		fmt.Println(e.word, e.count)
	}
	sum := 0
	for i, e := range sorted {
		sum = (sum*31 + e.count*(i+1)) % 1000003
	}
	fmt.Println(sum)
}
