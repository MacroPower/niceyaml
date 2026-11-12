// Package layermerge merges the layers of a niceyaml Source that
// NewSourceFromLayers builds.
//
// A [Reader] reads the document of each layer into a [Value], the way the
// go-yaml decoder reads it, with every alias and `<<` merge key resolved.
// [Value.Merge] puts the value of a higher layer over a lower one. A
// mapping merges key by key, a sequence or a scalar replaces what lies
// below it. A sequence that an [Appends] names adds its elements to the
// sequence below it instead. A written null without a tag keeps its key
// out of the merged value, in every layer above the lowest one that holds
// a value. Its entry stays in the value as a removed one, so an error
// about its key finds that null.
//
// Each value names the layer it came from by index, and
// [Value.ErrorLayer] names the layer that an error at it binds in, which
// prefers a layer whose errors report positions. [Value.Find] finds the
// value at a path. A sequence that several layers append to
// holds an element at another index than the layer of the element does.
// [Value.LayerPath] gives a path as one layer holds it, and
// [Value.Element] finds the element that a path goes through. [Text]
// writes the merged value as YAML.
package layermerge
