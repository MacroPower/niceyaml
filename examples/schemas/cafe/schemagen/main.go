// Package main generates a JSON schema for the cafe example.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
)

var outFile = flag.String("o", "schema.json", "Output file for the generated schema")

func main() {
	flag.Parse()

	js, err := jsonschema.GenerateFor[cafe.Config](
		context.Background(),
		jsonschema.WithDescriptionProvider(jsonschema.NewGoCommentProvider()),
	)
	if err != nil {
		log.Fatalf("generate JSON schema: %v", err)
	}

	jsData, err := json.MarshalIndent(js, "", "  ")
	if err != nil {
		log.Fatalf("marshal JSON schema: %v", err)
	}

	err = os.WriteFile(*outFile, jsData, 0o600)
	if err != nil {
		log.Fatalf("write schema file: %v", err)
	}
}
