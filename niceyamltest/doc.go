// Package niceyamltest provides checks for tests of code that extends
// niceyaml, such as a [niceyaml.Validator].
//
// A validator returns its errors bound through the Node it got, as
// [niceyaml.Validator] describes. [niceyaml.Node.Validate] and a decode
// bind what a validator leaves unbound, so a test that runs the validator
// through either one passes for a validator that binds nothing. Such a
// validator reports the wrong line when another validator calls its
// Validate method on each item of a list and returns the result, because
// the error then binds through the Node of the other validator.
//
// A test therefore calls Validate itself, on a Node from
// [niceyaml.Node.At], and passes the result to [CheckBound]:
//
//	func TestReservedName(t *testing.T) {
//		doc, err := niceyaml.NewSourceFromString(input).Document()
//		require.NoError(t, err)
//
//		item, err := doc.At(paths.Doc().Child("items").Index(1))
//		require.NoError(t, err)
//
//		err = rule.Validate(t.Context(), item)
//		require.NoError(t, niceyamltest.CheckBound(err))
//	}
//
// CheckBound returns an error, and the package imports neither testing nor
// an assertion library, so the test reports the result with whichever it
// uses.
package niceyamltest
