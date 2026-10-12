// File I/O as main.pts does it: write 2,000 small files 100 at a time (one goroutine each),
// read them back, stat every tenth, list the directory and remove it; three rounds.
package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	files = 2000
	batch = 100
)

func contentOf(i int) string {
	return fmt.Sprintf("file %d: %s\n", i, strings.Repeat("abcdefghij", i%40+1))
}

func round() int {
	dir, err := os.MkdirTemp("/tmp", "pts-bench-file-io-")
	if err != nil {
		panic(err)
	}
	path := func(i int) string { return fmt.Sprintf("%s/f%d.txt", dir, i) }
	for start := 0; start < files; start += batch {
		var wg sync.WaitGroup
		for i := start; i < start+batch; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := os.WriteFile(path(i), []byte(contentOf(i)), 0o666); err != nil {
					panic(err)
				}
			}(i)
		}
		wg.Wait()
	}
	total := 0
	for start := 0; start < files; start += batch {
		texts := make([]string, batch) // strings, as main.pts reads the files as text
		var wg sync.WaitGroup
		for i := start; i < start+batch; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				data, err := os.ReadFile(path(i))
				if err != nil {
					panic(err)
				}
				texts[i-start] = string(data)
			}(i)
		}
		wg.Wait()
		for _, text := range texts {
			total += len(text)
		}
		for i := start; i < start+batch; i += 10 {
			info, err := os.Stat(path(i))
			if err != nil {
				panic(err)
			}
			total += int(info.Size())
		}
	}
	listing, err := os.Open(dir)
	if err != nil {
		panic(err)
	}
	names, err := listing.Readdirnames(-1) // unsorted, as readdir's names are
	listing.Close()
	if err != nil {
		panic(err)
	}
	total += len(names)
	if err := os.RemoveAll(dir); err != nil {
		panic(err)
	}
	return total
}

func main() {
	sum := 0
	for i := 0; i < 3; i++ {
		sum += round()
	}
	fmt.Println(sum)
}
