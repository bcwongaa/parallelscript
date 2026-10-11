// N-body, the twin of main.pts: the energies are printed unrounded, as there.
package main

import (
	"fmt"
	"math"
	"strconv"
)

// Variables, not constants: Go would fold constant expressions exactly, where JS rounds
// every operation to float64.
var (
	pi          = math.Pi
	solarMass   = 4 * pi * pi
	daysPerYear = 365.24
)

type body struct {
	x, y, z, vx, vy, vz, mass float64
}

func offsetMomentum(bodies []*body) {
	var px, py, pz float64
	for _, b := range bodies {
		px += b.vx * b.mass
		py += b.vy * b.mass
		pz += b.vz * b.mass
	}
	bodies[0].vx = -px / solarMass
	bodies[0].vy = -py / solarMass
	bodies[0].vz = -pz / solarMass
}

func advance(bodies []*body, dt float64) {
	for i, bi := range bodies {
		for _, bj := range bodies[i+1:] {
			dx := bi.x - bj.x
			dy := bi.y - bj.y
			dz := bi.z - bj.z
			d2 := dx*dx + dy*dy + dz*dz
			mag := dt / (d2 * math.Sqrt(d2))
			bi.vx -= dx * bj.mass * mag
			bi.vy -= dy * bj.mass * mag
			bi.vz -= dz * bj.mass * mag
			bj.vx += dx * bi.mass * mag
			bj.vy += dy * bi.mass * mag
			bj.vz += dz * bi.mass * mag
		}
	}
	for _, b := range bodies {
		b.x += dt * b.vx
		b.y += dt * b.vy
		b.z += dt * b.vz
	}
}

func energy(bodies []*body) float64 {
	e := 0.0
	for i, bi := range bodies {
		e += 0.5 * bi.mass * (bi.vx*bi.vx + bi.vy*bi.vy + bi.vz*bi.vz)
		for _, bj := range bodies[i+1:] {
			dx := bi.x - bj.x
			dy := bi.y - bj.y
			dz := bi.z - bj.z
			e -= (bi.mass * bj.mass) / math.Sqrt(dx*dx+dy*dy+dz*dz)
		}
	}
	return e
}

func main() {
	const steps = 5000000
	bodies := []*body{
		{0, 0, 0, 0, 0, 0, solarMass},
		{4.8414314424647209, -1.16032004402742839, -1.03622044471123109e-1,
			1.66007664274403694e-3 * daysPerYear, 7.69901118419740425e-3 * daysPerYear, -6.90460016972063023e-5 * daysPerYear,
			9.54791938424326609e-4 * solarMass},
		{8.34336671824457987, 4.12479856412430479, -4.03523417114321381e-1,
			-2.76742510726862411e-3 * daysPerYear, 4.99852801234917238e-3 * daysPerYear, 2.30417297573763929e-5 * daysPerYear,
			2.85885980666130812e-4 * solarMass},
		{1.2894369562139131e1, -1.51111514016986312e1, -2.23307578892655734e-1,
			2.96460137564761618e-3 * daysPerYear, 2.3784717395948095e-3 * daysPerYear, -2.96589568540237556e-5 * daysPerYear,
			4.36624404335156298e-5 * solarMass},
		{1.53796971148509165e1, -2.59193146099879641e1, 1.79258772950371181e-1,
			2.68067772490389322e-3 * daysPerYear, 1.62824170038242295e-3 * daysPerYear, -9.5159225451971587e-5 * daysPerYear,
			5.15138902046611451e-5 * solarMass},
	}
	offsetMomentum(bodies)
	fmt.Println(strconv.FormatFloat(energy(bodies), 'f', -1, 64))
	for i := 0; i < steps; i++ {
		advance(bodies, 0.01)
	}
	fmt.Println(strconv.FormatFloat(energy(bodies), 'f', -1, 64))
}
