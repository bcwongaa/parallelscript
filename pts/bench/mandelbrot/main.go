// The Mandelbrot set, the twin of main.pts: rows rendered by a goroutine per core.
package main

import (
	"fmt"
	"runtime"
	"sync"
)

const (
	width         = 2400
	height        = 1600
	maxIterations = 500
)

func escapeTime(cx, cy float64) uint16 {
	x, y := 0.0, 0.0
	i := 0
	for i < maxIterations && x*x+y*y <= 4 {
		t := x*x - y*y + cx
		y = 2*x*y + cy
		x = t
		i++
	}
	return uint16(i)
}

func main() {
	image := make([]uint16, width*height)
	rows := make(chan int, height)
	for py := 0; py < height; py++ {
		rows <- py
	}
	close(rows)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for py := range rows {
				cy := float64(py)/height*2.4 - 1.2
				for px := 0; px < width; px++ {
					image[py*width+px] = escapeTime(float64(px)/width*3.5-2.5, cy)
				}
			}
		}()
	}
	wg.Wait()
	total, inside := 0, 0 // two passes, as main.pts's .par.sum() and .par.count
	for _, n := range image {
		total += int(n)
	}
	for _, n := range image {
		if n == maxIterations {
			inside++
		}
	}
	fmt.Println(total, inside)
}
