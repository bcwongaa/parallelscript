// Sum and reduce over 100 million numbers, the twin of main.pts: a goroutine per core
// fills, sums, reduces and finds the largest value of its part of a []float64.
package main

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"sync"
)

const (
	count  = 100_000_000
	rounds = 2
)

// parts runs piece(from, to) on a goroutine per core over 0..count and returns their results in order.
func parts(piece func(from, to int) float64) []float64 {
	workers := runtime.GOMAXPROCS(0)
	results := make([]float64, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			results[w] = piece(count*w/workers, count*(w+1)/workers)
		}(w)
	}
	wg.Wait()
	return results
}

func main() {
	values := make([]float64, count)
	parts(func(from, to int) float64 {
		for i := from; i < to; i++ {
			values[i] = float64(i&1023) * 0.25
		}
		return 0
	})
	sum, reduced, max := 0.0, 0.0, 0.0
	for round := 0; round < rounds; round++ {
		for _, part := range parts(func(from, to int) float64 {
			total := 0.0
			for _, v := range values[from:to] {
				total += v
			}
			return total
		}) {
			sum += part
		}
		for _, part := range parts(func(from, to int) float64 {
			total := 0.0
			for _, v := range values[from:to] {
				total = total + v
			}
			return total
		}) {
			reduced += part
		}
		for _, part := range parts(func(from, to int) float64 {
			largest := math.Inf(-1)
			for _, v := range values[from:to] {
				largest = math.Max(largest, v)
			}
			return largest
		}) {
			max = math.Max(max, part)
		}
	}
	format := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	fmt.Println(format(sum), format(reduced), format(max))
}
