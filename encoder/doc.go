// Package encoder writes Go values as YAML.
//
// An [Encoder] wraps the go-yaml encoder so its settings are named options
// of this module. Create one with [New] and call [Encoder.Encode] for each
// value:
//
//	enc := encoder.New(os.Stdout, encoder.Pretty()...)
//	if err := enc.Encode(cfg); err != nil {
//		return err
//	}
//
// [Pretty] returns the options for two-space indentation with indented
// sequences, which is the layout prettier produces. [WithYAMLOptions]
// passes go-yaml options through for settings that have no option of their
// own.
package encoder
