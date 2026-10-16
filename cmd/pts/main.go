// Command pts is the ParallelScript compiler.
package main

import (
	"context"
	"os"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("pts: " + err.Error() + "\n")
		os.Exit(exitFailure)
	}
	c := &cli{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, cwd: cwd, debug: os.Getenv("PTS_DEBUG") == "1"}
	os.Exit(c.run(context.Background(), os.Args[1:]))
}
