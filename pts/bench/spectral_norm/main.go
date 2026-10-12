// Spectral norm, the twin of main.pts: the matrix's entries are computed in float64, as
// JS computes them, and the norm is printed unrounded, as there.
package main

import (
	"fmt"
	"math"
	"strconv"
)

const n = 4000

func a(i, j float64) float64 {
	return 1 / ((i+j)*(i+j+1)/2 + i + 1)
}

// multiplyAv sets out to A v.
func multiplyAv(v, out []float64) {
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += a(float64(i), float64(j)) * v[j]
		}
		out[i] = sum
	}
}

// multiplyAtv sets out to Aᵀ v.
func multiplyAtv(v, out []float64) {
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += a(float64(j), float64(i)) * v[j]
		}
		out[i] = sum
	}
}

// multiplyAtAv sets out to Aᵀ A v, with tmp as room for A v.
func multiplyAtAv(v, out, tmp []float64) {
	multiplyAv(v, tmp)
	multiplyAtv(tmp, out)
}

func main() {
	u := make([]float64, n)
	for i := range u {
		u[i] = 1
	}
	v := make([]float64, n)
	tmp := make([]float64, n)
	for round := 0; round < 10; round++ {
		multiplyAtAv(u, v, tmp)
		multiplyAtAv(v, u, tmp)
	}
	vBv, vv := 0.0, 0.0
	for i := 0; i < n; i++ {
		vBv += u[i] * v[i]
		vv += v[i] * v[i]
	}
	fmt.Println(strconv.FormatFloat(math.Sqrt(vBv/vv), 'f', -1, 64))
}
