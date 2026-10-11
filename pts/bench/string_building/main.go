// String building, the twin of main.pts: Go appends with a strings.Builder (its `+=`
// copies the whole string every time), each piece made as main.pts makes it.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

func appended(n int) string {
	var s strings.Builder
	for i := 0; i < n; i++ {
		s.WriteString("item" + strconv.Itoa(i) + ";")
	}
	return s.String()
}

func report(n int) string {
	var out strings.Builder
	for i := 0; i < n; i++ {
		kind := "other"
		if i%7 == 0 {
			kind = "seven"
		}
		out.WriteString("line " + strconv.Itoa(i) + ": " + kind + " " + strconv.Itoa(i*3) + "\n")
	}
	return out.String()
}

func joined(n int) string {
	parts := []string{}
	for i := 0; i < n; i++ {
		parts = append(parts, "p"+strconv.Itoa(i%100))
	}
	return strings.Join(parts, ",")
}

func checksum(s string) int {
	sum := 0
	for i := 0; i < len(s); i += 7 {
		sum = (sum*31 + int(s[i])) % 1000003
	}
	return sum
}

func main() {
	const n = 1000000
	for _, s := range []string{appended(n), report(n), joined(n)} {
		fmt.Println(len(s), checksum(s))
	}
}
