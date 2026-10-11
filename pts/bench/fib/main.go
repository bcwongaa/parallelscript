// Recursive Fibonacci on float64, the twin of main.pts.
package main

import (
	"fmt"
	"strconv"
)

func fib(n float64) float64 {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	fmt.Println(strconv.FormatFloat(fib(38), 'f', -1, 64))
}
