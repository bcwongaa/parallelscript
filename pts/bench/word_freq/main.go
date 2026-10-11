// Word frequency, the twin of main.pts: the same text, split with strings.Split and
// counted in a map[string]int, sorted with sort.Slice.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

var syllables = []string{"ka", "lo", "mi", "ne", "ru", "so", "ta", "vi", "ze", "po", "qua"}

func word(i int) string {
	w := syllables[i%11]
	for rest := i / 11; rest > 0; rest /= 11 {
		w += syllables[rest%11]
	}
	return w
}

func text(words, vocabulary int) string {
	parts := []string{}
	seed := 42
	for i := 0; i < words; i++ {
		seed = (seed * 48271) % 2147483647
		r := float64(seed) / 2147483647
		parts = append(parts, word(int(math.Floor(r*r*float64(vocabulary)))))
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
