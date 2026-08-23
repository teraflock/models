// Command validate checks every catalog manifest and fingerprint prompt set
// in this repository against the JSON Schemas in schema/ and runs the
// cross-checks that a schema cannot express (payout class vs params_b,
// pricing table conformance, min_vram sanity vs artifact size, quant naming
// vs artifact URL, fingerprint set references).
//
// Usage: go run . -root ../..
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "path to the models repo root")
	emitFlat := flag.String("emit-flat", "", "after validating, write the flat flockd-format catalog JSON here")
	flag.Parse()

	issues, err := Run(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate: %v\n", err)
		os.Exit(2)
	}
	for _, is := range issues {
		fmt.Fprintf(os.Stderr, "FAIL: %s\n", is)
	}
	if len(issues) > 0 {
		fmt.Fprintf(os.Stderr, "%d issue(s) found\n", len(issues))
		os.Exit(1)
	}
	fmt.Println("catalog OK")

	if *emitFlat != "" {
		buf, err := EmitFlat(*root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "emit-flat: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(*emitFlat, append(buf, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "emit-flat: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s\n", *emitFlat)
	}
}
