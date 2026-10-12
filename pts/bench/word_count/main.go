// Word count over many files, the twin of main.pts: the same 64 generated files (made
// once, as main.pts makes them), counted by a goroutine per core into maps, then merged.
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	dir          = "/tmp/pts-bench-word-count-1"
	files        = 64
	wordsPerFile = 100_000
	vocabulary   = 5_000
)

var syllables = []string{"ka", "lo", "mi", "ne", "ru", "sa", "to", "vi", "ze", "po", "an", "el"}

// Park and Miller's generator, as main.pts computes it in doubles.
var seed int64 = 42

func next() int64 {
	seed = seed * 48271 % 2147483647
	return seed
}

func word(index int) string {
	text := ""
	for n := index + 1; n > 0; n /= len(syllables) {
		text += syllables[n%len(syllables)]
	}
	return text
}

func generate() {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	words := make([]string, vocabulary)
	for i := range words {
		words[i] = word(i)
	}
	for f := 0; f < files; f++ {
		var lines []string
		var line []string
		for w := 0; w < wordsPerFile; w++ {
			r := float64(next()) / 2147483647
			line = append(line, words[int(math.Floor(r*r*r*vocabulary))])
			if len(line) == 12 {
				lines = append(lines, strings.Join(line, " ")+".")
				line = nil
			}
		}
		lines = append(lines, strings.Join(line, " "))
		if err := os.WriteFile(fmt.Sprintf("%s/%d.txt", dir, f), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			panic(err)
		}
	}
	if err := os.WriteFile(dir+"/done", nil, 0o644); err != nil {
		panic(err)
	}
}

func countWords(path string) map[string]int {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	text := string(data)
	counts := map[string]int{}
	start := -1
	for i := 0; i <= len(text); i++ {
		letter := i < len(text) && text[i] >= 'a' && text[i] <= 'z'
		if letter && start < 0 {
			start = i
		} else if !letter && start >= 0 {
			counts[text[start:i]]++
			start = -1
		}
	}
	return counts
}

func main() {
	if _, err := os.Stat(dir + "/done"); err != nil {
		generate()
	}
	perFile := make([]map[string]int, files)
	paths := make(chan int, files)
	for f := 0; f < files; f++ {
		paths <- f
	}
	close(paths)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range paths {
				perFile[f] = countWords(fmt.Sprintf("%s/%d.txt", dir, f))
			}
		}()
	}
	wg.Wait()
	totals := map[string]int{}
	all := 0
	for _, counts := range perFile {
		for found, n := range counts {
			totals[found] += n
		}
	}
	for _, n := range totals {
		all += n
	}
	type entry struct {
		word  string
		count int
	}
	entries := make([]entry, 0, len(totals))
	for found, n := range totals {
		entries = append(entries, entry{found, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].word < entries[j].word
	})
	top := make([]string, 0, 5)
	for _, e := range entries[:5] {
		top = append(top, fmt.Sprintf("%s:%d", e.word, e.count))
	}
	fmt.Println(len(totals), all, strings.Join(top, " "))
}
