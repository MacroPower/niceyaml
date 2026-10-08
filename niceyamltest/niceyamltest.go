package niceyamltest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.jacobcolvin.com/niceyaml"
)

// ErrUnbound indicates an error bound to no source. [CheckBound] returns
// it with the message of each such error.
var ErrUnbound = errors.New("bound to no source")

// CheckBound returns nil when err is bound, as [niceyaml.Validator] asks
// of every error a validator returns, and an error that wraps
// [ErrUnbound] when it is not. A test calls the Validate method of the
// validator itself, on a Node from [niceyaml.Node.At], and passes the
// result:
//
//	item, err := doc.At(paths.Doc().Child("items").Index(1))
//	require.NoError(t, err)
//
//	require.NoError(t, niceyamltest.CheckBound(rule.Validate(ctx, item)))
//
// An error is unbound when [niceyaml.Node.Bind] would give it a source: an
// [*niceyaml.Error] that no Node bound, a plain error, the result of
// [niceyaml.Rebase] or [niceyaml.BindValue], and an Error that puts a
// location above a bound error. CheckBound reads err as
// [niceyaml.NewErrorTree] lays it out and checks every error of the tree.
// It thus finds an unbound error that [errors.Join] holds beside bound
// ones, and an unbound detail of a bound error. The join itself carries no
// message and needs no source, so a join of bound errors passes.
//
// The result quotes the message of each unbound error, as in
// `bound to no source: "reserved name"`. An err that is nil, or that holds
// a nil [*niceyaml.Error] or [*niceyaml.SourceError] pointer, is no error
// and passes.
func CheckBound(err error) error {
	// A bound error keeps the source that bound it, so an error that takes
	// this source had none.
	probe := niceyaml.NewSourceFromString("")

	var unbound []string

	for node := range niceyaml.NewErrorTree(probe.Bind(err)).All() {
		if node.Bound != nil && node.Bound.Source() == probe {
			unbound = append(unbound, strconv.Quote(node.Message()))
		}
	}

	if len(unbound) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrUnbound, strings.Join(unbound, ", "))
}
