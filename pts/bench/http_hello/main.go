// HTTP hello world, the twin of main.pts on Go's net/http, which serves each connection
// on a goroutine over every core.
package main

import (
	"cmp"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
		w.Write([]byte("Hello, World!"))
	})
	http.ListenAndServe(":"+cmp.Or(os.Getenv("PORT"), "3000"), nil)
}
