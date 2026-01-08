// Package main generates a JSON schema for the cafe example.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/macropower/niceyaml/examples/schemas/cafe"
	"github.com/macropower/niceyaml/schema/generate"
)

var outFile = flag.String("o", "schema.json", "Output file for the generated schema")

func main() {
	flag.Parse()

	gen := generate.NewGenerator(
		cafe.NewConfig(),
		generate.WithPackagePaths(
			"github.com/macropower/niceyaml/examples/schemas/cafe",
		),
	)
	jsData, err := gen.Generate()
	if err != nil {
		log.Fatalf("generate JSON schema: %v", err)
	}

	// Write schema.json file.
	err = os.WriteFile(*outFile, jsData, 0o600)
	if err != nil {
		log.Fatalf("write schema file: %v", err)
	}
}
