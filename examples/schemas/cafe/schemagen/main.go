// Package main generates a JSON schema for the cafe example.
package main

import (
	"flag"
	"log"
	"os"

	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
	"go.jacobcolvin.com/niceyaml/schema/generator"
)

var outFile = flag.String("o", "schema.json", "Output file for the generated schema")

func main() {
	flag.Parse()

	gen := generator.New(
		cafe.NewConfig(),
		generator.WithPackagePaths(
			"go.jacobcolvin.com/niceyaml/examples/schemas/cafe",
			"go.jacobcolvin.com/niceyaml/examples/schemas/cafe/spec",
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
