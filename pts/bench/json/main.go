// JSON, the twin of main.pts: encoding/json with structs for the typed records and
// []any of map[string]any for the parsed data (whose keys Go sorts, so only lengths of
// its text are compared).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

type position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type item struct {
	ID       float64  `json:"id"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	Score    float64  `json:"score"`
	Active   bool     `json:"active"`
	Position position `json:"position"`
}

func makeItems(n, round int) []item {
	items := []item{}
	for i := 0; i < n; i++ {
		k := i + round
		items = append(items, item{
			ID:       float64(k),
			Name:     "item-" + strconv.Itoa(k),
			Tags:     []string{"t" + strconv.Itoa(k%7), "group" + strconv.Itoa(k%13)},
			Score:    float64((k*37)%1000) / 8,
			Active:   k%3 == 0,
			Position: position{float64(k % 100), float64((k * 7) % 100)},
		})
	}
	return items
}

func checksum(s []byte) int {
	sum := 0
	for i := 0; i < len(s); i += 13 {
		sum = (sum*31 + int(s[i])) % 1000003
	}
	return sum
}

func marshal(v any, indent bool) []byte {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

func main() {
	const n = 50000
	for round := 0; round < 3; round++ {
		text := marshal(makeItems(n, round), false)
		var data []any
		if err := json.Unmarshal(text, &data); err != nil {
			panic(err)
		}
		score, active, tags := 0.0, 0, 0.0
		for _, element := range data {
			item := element.(map[string]any)
			score += item["score"].(float64)
			if item["active"].(bool) {
				active++
			}
			tags += float64(len(item["tags"].([]any))) + item["position"].(map[string]any)["x"].(float64)
		}
		again := marshal(data, false)
		pretty := marshal(data, true)
		fmt.Println(len(text), checksum(text), strconv.FormatFloat(score, 'f', -1, 64), active, strconv.FormatFloat(tags, 'f', -1, 64), len(again), len(pretty))
	}
}
