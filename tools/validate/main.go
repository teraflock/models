// Command validate checks every catalog manifest and fingerprint prompt set
// in this repository against the JSON Schemas in schema/ and runs the
// cross-checks that a schema cannot express (payout class vs params_b,
// pricing table conformance, min_vram sanity vs artifact size, quant naming
// vs artifact URL, fingerprint set references).
//
// Usage: go run . -root ../..
//
// With -release every TODO-verify placeholder is an error (and -emit-flat
// refuses to write): that is the mode the promote workflow runs before
// writing catalog/catalog.json, the object every fresh flockd install reads.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "path to the models repo root")
	emitFlat := flag.String("emit-flat", "", "after validating, write the flat flockd-format catalog JSON here")
	release := flag.Bool("release", false, "release mode: reject every TODO-verify placeholder (required before publishing catalog/catalog.json)")
	flag.Parse()

	opts := Options{Release: *release}
	issues, err := RunWith(*root, opts)
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
		buf, err := EmitFlatWith(*root, opts)
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
