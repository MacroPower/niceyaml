package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

var (
	//go:embed shared.yaml
	exampleShared string

	//go:embed order.yaml
	exampleOrder string

	//go:embed te.yaml
	exampleTe string

	//go:embed bomb.yaml
	exampleBomb string

	//go:embed deep.yaml
	exampleDeep string

	// A stock lookup returns errOffline when the service does not answer.
	errOffline = errors.New("stock service does not answer")
)

// kronor is a price that the document writes as text, such as "35 kr".
type kronor int

// UnmarshalText reads a price such as "35 kr".
func (k *kronor) UnmarshalText(text []byte) error {
	var n int

	_, err := fmt.Sscanf(string(text), "%d kr", &n)
	if err != nil {
		return fmt.Errorf("read price %q: %w", text, err)
	}

	*k = kronor(n)

	return nil
}

// item is one entry of an order.
type item struct {
	Item  string `yaml:"item"`
	Note  string `yaml:"note"`
	Price kronor `yaml:"price"`
}

// order is the type each upload decodes into.
type order struct {
	Items []item `yaml:"items"`
	Table int    `yaml:"table"`
}

// stock holds how many of each item the cafe has left. A real server asks
// a stock service over the network, and offline simulates a service that
// does not answer.
type stock struct {
	left    map[string]int
	offline bool
}

// inStock rejects an item that the cafe has sold out of. A lookup that
// fails says nothing about the order, so inStock returns its error inside
// Unchecked at the item that it looked up.
func (s stock) inStock(_ context.Context, it *item) error {
	at := niceyaml.AtPath(paths.Current().Child("item"))

	if s.offline {
		return niceyaml.Unchecked(fmt.Errorf("look up %s: %w", it.Item, errOffline), at)
	}

	if s.left[it.Item] == 0 {
		return niceyaml.NewError(it.Item+" is sold out", at)
	}

	return nil
}

func main() {
	ctx := context.Background()
	p := printer.New()
	open := stock{left: map[string]int{"Kaffe": 40, "Te": 0, "Kanelbulle": 12}}

	// The alias limit counts the aliases of each upload but not those of its
	// reference documents. The server thus passes only files that it trusts
	// as reference documents, such as the shared file that it ships.
	shared := niceyaml.NewSourceFromString(exampleShared, niceyaml.WithName("shared.yaml"))

	fmt.Println("\nAccept an order that names shared anchors:")

	// The upload does not define any anchor, so each of its aliases reads
	// an anchor of the shared file.
	serve(ctx, p, shared, open, "order.yaml", exampleOrder)

	fmt.Println("\nBind an error in a shared value at its alias:")

	// The upload does not hold a line for the value of *te, so the error
	// about it binds at the alias.
	serve(ctx, p, shared, open, "te.yaml", exampleTe)

	fmt.Println("\nRefuse an alias bomb:")

	// Before the decoder starts, the decode counts what the aliases make a
	// reader read. A price reads its own text. Without the limit, the go-yaml
	// decoder thus writes out a billion fika to read the one price of this
	// upload. It never checks the context, so a deadline does not stop it.
	// The error binds at the alias that repeats the most, the one price of
	// the upload.
	serve(ctx, p, shared, open, "bomb.yaml", exampleBomb)

	fmt.Println("\nRefuse a document that nests too deep:")

	// The memory that the go-yaml parser keeps grows with the square of
	// the depth, so the Source counts the levels before the parser runs.
	// The error binds at the bracket that opens the ninth level.
	serve(ctx, p, shared, open, "deep.yaml", exampleDeep)

	fmt.Println("\nRefuse an upload past the byte limit:")

	// NewSourceFromReader reads at most one byte past the limit, so the
	// memory that the read takes grows with the limit rather than with the
	// body.
	large := "table: 7\nitems:\n" + strings.Repeat("  - *kaffe\n", 500)
	serve(ctx, p, shared, open, "large.yaml", large)

	fmt.Println("\nRefuse an upload whose aliases repeat too much text:")

	// A price reads its own text. The decode thus bounds the text that
	// every alias of the upload repeats, even an alias of a note, which is
	// a plain string. The upload holds less than 4 KiB, but its aliases
	// repeat more text than that.
	notes := "table: 7\nitems:\n" +
		"  - {item: Kaffe, price: 35 kr, note: &note " + strings.Repeat("kanel ", 40) + "}\n" +
		strings.Repeat("  - {item: Kaffe, price: 35 kr, note: *note}\n", 20)
	serve(ctx, p, shared, open, "notes.yaml", notes)

	fmt.Println("\nAnswer 503 when the stock lookup fails:")

	// The error of the lookup binds at the alias, as the error about Te
	// does. Unchecked declares that the check could not run, so IsInvalid
	// does not report the error and the status is 503.
	serve(ctx, p, shared, stock{offline: true}, "bulle.yaml", "table: 2\nitems:\n  - *kanelbulle\n")

	fmt.Println("\nStop when the client disconnects:")

	// The decode checks the context before the decoder starts. IsInvalid
	// does not report the error of a context that ended, so the status is
	// 500.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	serve(gone, p, shared, open, "order.yaml", exampleOrder)
}

// serve reads one upload as a handler reads the body of a request, and
// prints the order or the error with its status code.
func serve(ctx context.Context, p *printer.Printer, shared *niceyaml.Source, s stock, name, body string) {
	o, err := decode(ctx, shared, s, name, strings.NewReader(body))
	if err != nil {
		lipgloss.Println(p.PrintError(err))
	} else {
		var total kronor

		for _, it := range o.Items {
			total += it.Price
		}

		fmt.Printf("table %d orders %d items for %d kr\n", o.Table, len(o.Items), total)
	}

	code := status(err)
	fmt.Println(code, http.StatusText(code))
}

// decode reads an order from the body of an upload.
func decode(ctx context.Context, shared *niceyaml.Source, s stock, name string, body io.Reader) (order, error) {
	// An order nests three levels and fits in far less than 4 KiB. The
	// server thus sets the byte and depth limits far lower than their
	// defaults. The alias limit measures what the aliases make a decode
	// read against what the upload holds, so it does not take a size. The
	// byte and depth limits also bound the text that the aliases write
	// out. The server thus sizes those two and keeps the alias limit on.
	source, err := niceyaml.NewSourceFromReader(body,
		niceyaml.WithName(name),
		niceyaml.WithByteLimit(4<<10),
		niceyaml.WithDepthLimit(8),
		niceyaml.WithAliasLimit(true),
		niceyaml.WithReferences(shared),
	)
	if err != nil {
		return order{}, err
	}

	return source.Decode[order](ctx, niceyaml.WithValueCheck(s.inStock))
}

// status picks the status code of a response to err.
func status(err error) int {
	switch {
	case err == nil:
		return http.StatusOK

	// An error can hold several problems, and errors.Is reports whether
	// any one of them matches. Ask IsInvalid first, so that status returns
	// 5xx for a fault of the server even when the error also holds a fault
	// of the document. The error of a stock service that does not answer
	// gets 503, which tells the client that the server could not check the
	// stock.
	case !niceyaml.IsInvalid(err) && errors.Is(err, errOffline):
		return http.StatusServiceUnavailable
	case !niceyaml.IsInvalid(err):
		return http.StatusInternalServerError

	// An upload past a size limit of its Source matches ErrLimit, and an
	// alias bomb matches ErrExcessiveAliasing. The server answers 413 for
	// both. A document that nests too deep does not parse, but its error
	// matches ErrLimit and not ErrSyntax.
	case errors.Is(err, niceyaml.ErrLimit), errors.Is(err, niceyaml.ErrExcessiveAliasing):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, niceyaml.ErrSyntax):
		return http.StatusBadRequest
	default:
		return http.StatusUnprocessableEntity
	}
}
