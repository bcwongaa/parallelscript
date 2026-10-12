// Fan-out of CPU-heavy tasks, the twin of main.pts: the same 64 tasks of uneven sizes,
// a goroutine each.
package main

import (
	"fmt"
	"math"
	"strconv"
	"sync"
)

const (
	tasks   = 64
	million = 1_000_000
)

func series(task int) float64 {
	terms := (task%8 + 1) * million
	sum := 0.0
	for k := 1; k <= terms; k++ {
		fk := float64(k)
		sum += math.Sqrt(fk+float64(task)) / (fk*fk + 1)
	}
	return sum
}

func main() {
	sums := make([]float64, tasks)
	var wg sync.WaitGroup
	for task := 0; task < tasks; task++ {
		wg.Add(1)
		go func(task int) {
			defer wg.Done()
			sums[task] = series(task)
		}(task)
	}
	wg.Wait()
	total, largest := 0.0, math.Inf(-1)
	for _, sum := range sums {
		total += sum
		largest = math.Max(largest, sum)
	}
	fmt.Println(strconv.FormatFloat(total, 'f', -1, 64), strconv.FormatFloat(largest, 'f', -1, 64))
}
