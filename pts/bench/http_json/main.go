// HTTP JSON, the twin of main.pts on Go's net/http (a goroutine per connection over
// every core) with encoding/json and structs.
package main

import (
	"cmp"
	"encoding/json"
	"net/http"
	"os"
)

type item struct {
	Sku      string  `json:"sku"`
	Quantity float64 `json:"quantity"`
	Price    float64 `json:"price"`
}

type order struct {
	ID       float64 `json:"id"`
	Customer string  `json:"customer"`
	Items    []item  `json:"items"`
}

type summary struct {
	ID       float64 `json:"id"`
	Customer string  `json:"customer"`
	Items    int     `json:"items"`
	Total    float64 `json:"total"`
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var o order
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		total := 0.0
		for _, it := range o.Items {
			total += it.Quantity * it.Price
		}
		body, _ := json.Marshal(summary{ID: o.ID, Customer: o.Customer, Items: len(o.Items), Total: total})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	http.ListenAndServe(":"+cmp.Or(os.Getenv("PORT"), "3000"), nil)
}
