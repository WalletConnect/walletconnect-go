// Command wctrace reads the SDK's trace file.
//
//	wctrace summary walletconnect.trace.jsonl
//
// summary adds up the file and its rotated predecessors (.2 then .1 then
// the file itself) into counts and latencies per span name and method.
package main

import (
	"fmt"
	"os"

	"github.com/WalletConnect/walletconnect-go/tracefile"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "summary" {
		fmt.Fprintln(os.Stderr, "usage: wctrace summary <trace file> [more files]")
		os.Exit(2)
	}
	var files []string
	for _, p := range os.Args[2:] {
		files = append(files, tracefile.Files(p)...)
	}
	sum, err := tracefile.SummarizeFiles(files...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wctrace:", err)
		os.Exit(1)
	}
	fmt.Print(sum.Format())
}
