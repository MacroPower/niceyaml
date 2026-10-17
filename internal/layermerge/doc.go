// Package layermerge merges the layers of a niceyaml Source that
// NewSourceFromLayers builds.
//
// A [Reader] reads the document of each layer into a [Value], the way the
// go-yaml decoder reads it, with every alias and `<<` merge key resolved.
// [Value.Merge] puts the value of a higher layer over a lower one. A
// mapping merges key by key, a sequence or a scalar replaces what lies
// below it, and a written null without a tag removes its key. A removed
// entry stays in the value, so an error about its key finds the null that
// removed it. Each value names the layer it came from by index, and
// [Value.Find] finds the value at a path. [Text] writes the merged value
// as YAML.
package layermerge
