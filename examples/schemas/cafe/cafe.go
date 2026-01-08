// Package cafe is an example.
package cafe

import (
	"encoding/json"
	"time"

	"github.com/invopop/jsonschema"

	_ "embed"

	"github.com/macropower/niceyaml/schema/validate"
)

//go:generate go run ./schemagen/main.go -o cafe.v1.json

var (
	//go:embed cafe.v1.json
	schemaJSON []byte

	// DefaultValidator validates configuration against the JSON schema.
	DefaultValidator = validate.MustNewValidator("/cafe.v1.json", schemaJSON)
)

// Config is the root cafe configuration.
// Create instances with [NewConfig].
type Config struct {
	// Kind identifies this configuration type.
	Kind string `json:"kind" jsonschema:"title=Kind"`
	// Metadata contains identifying information about the cafe.
	Metadata Metadata `json:"metadata" jsonschema:"title=Metadata"`
	// Spec contains the cafe specification.
	Spec Spec `json:"spec" jsonschema:"title=Spec"`
}

// NewConfig creates a new [Config].
func NewConfig() Config {
	return Config{}
}

// JSONSchemaExtend extends the generated JSON schema.
func (c Config) JSONSchemaExtend(js *jsonschema.Schema) {
	kind, ok := js.Properties.Get("kind")
	if !ok {
		panic("kind property not found in schema")
	}

	kind.Const = "Config"
	js.Properties.Set("kind", kind)
}

// Validate extends schema-driven validation with custom validation logic.
func (c Config) Validate() error {
	return nil
}

// Metadata contains identifying information about the cafe.
type Metadata struct {
	// Name is the name of the cafe.
	Name string `json:"name" jsonschema:"title=Name"`
	// Description provides additional details about the cafe.
	Description string `json:"description,omitempty" jsonschema:"title=Description"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (m Metadata) JSONSchemaExtend(js *jsonschema.Schema) {
	name, ok := js.Properties.Get("name")
	if !ok {
		panic("name property not found in schema")
	}

	name.MinLength = ptrUint64(1)
	name.MaxLength = ptrUint64(100)
	js.Properties.Set("name", name)
}

// Spec is the cafe specification.
type Spec struct {
	// SLA is the service level agreement duration for order fulfillment.
	// Defaults to 15 minutes.
	SLA *time.Duration `json:"sla,omitempty" jsonschema:"title=SLA,type=string,default=15m"`
	// Settings contains optional cafe settings.
	Settings *Settings `json:"settings,omitempty" jsonschema:"title=Settings"`
	// Hours defines operating hours.
	Hours Hours `json:"hours" jsonschema:"title=Hours"`
	// Menu defines the cafe's menu.
	Menu Menu `json:"menu" jsonschema:"title=Menu"`
	// Staff defines staffing requirements.
	Staff Staff `json:"staff" jsonschema:"title=Staff"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (s Spec) JSONSchemaExtend(js *jsonschema.Schema) {
	sla, ok := js.Properties.Get("sla")
	if !ok {
		panic("sla property not found in schema")
	}

	sla.Pattern = `^(\d+d)?(\d+h)?(\d+m)?(\d+s)?$`
	js.Properties.Set("sla", sla)
}

// Menu defines the cafe's menu offerings.
type Menu struct {
	// Items is the list of menu items.
	Items []MenuItem `json:"items" jsonschema:"title=Items"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (m Menu) JSONSchemaExtend(js *jsonschema.Schema) {
	items, ok := js.Properties.Get("items")
	if !ok {
		panic("items property not found in schema")
	}

	items.MinItems = ptrUint64(1)
	js.Properties.Set("items", items)
}

// MenuItem represents a single item on the menu.
type MenuItem struct {
	// Available indicates whether the item is currently available.
	Available *bool `json:"available,omitempty" jsonschema:"title=Available,default=true"`
	// Name is the name of the menu item.
	Name string `json:"name" jsonschema:"title=Name"`
	// Category is the type of item.
	Category string `json:"category" jsonschema:"title=Category,enum=coffee,enum=tea,enum=pastry,enum=sandwich"`
	// Description provides additional details about the item.
	Description string `json:"description,omitempty" jsonschema:"title=Description"`
	// Tags are optional labels for the item.
	Tags []string `json:"tags,omitempty" jsonschema:"title=Tags"`
	// Price is the cost of the item in dollars.
	Price float64 `json:"price" jsonschema:"title=Price"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (m MenuItem) JSONSchemaExtend(js *jsonschema.Schema) {
	name, ok := js.Properties.Get("name")
	if !ok {
		panic("name property not found in schema")
	}

	name.MinLength = ptrUint64(1)
	js.Properties.Set("name", name)

	price, ok := js.Properties.Get("price")
	if !ok {
		panic("price property not found in schema")
	}

	price.Minimum = json.Number("0")
	js.Properties.Set("price", price)
}

// Staff defines staffing requirements.
type Staff struct {
	// Baristas is the number of baristas on shift.
	Baristas int `json:"baristas" jsonschema:"title=Baristas,default=2"`
	// Managers is the number of managers on shift.
	Managers int `json:"managers" jsonschema:"title=Managers,default=1"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (s Staff) JSONSchemaExtend(js *jsonschema.Schema) {
	baristas, ok := js.Properties.Get("baristas")
	if !ok {
		panic("baristas property not found in schema")
	}

	baristas.Minimum = json.Number("1")
	baristas.Maximum = json.Number("10")
	js.Properties.Set("baristas", baristas)

	managers, ok := js.Properties.Get("managers")
	if !ok {
		panic("managers property not found in schema")
	}

	managers.Minimum = json.Number("1")
	js.Properties.Set("managers", managers)
}

// Hours defines operating hours for the cafe.
type Hours struct {
	// Open is the opening time in HH:MM format (24-hour).
	Open string `json:"open" jsonschema:"title=Open"`
	// Close is the closing time in HH:MM format (24-hour).
	Close string `json:"close" jsonschema:"title=Close"`
	// Days lists the days of operation.
	Days []string `json:"days" jsonschema:"title=Days,enum=monday,enum=tuesday,enum=wednesday,enum=thursday,enum=friday,enum=saturday,enum=sunday"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (h Hours) JSONSchemaExtend(js *jsonschema.Schema) {
	timePattern := `^([01]?[0-9]|2[0-3]):[0-5][0-9]$`

	open, ok := js.Properties.Get("open")
	if !ok {
		panic("open property not found in schema")
	}

	open.Pattern = timePattern
	open.Default = "07:00"
	js.Properties.Set("open", open)

	closeTime, ok := js.Properties.Get("close")
	if !ok {
		panic("close property not found in schema")
	}

	closeTime.Pattern = timePattern
	closeTime.Default = "19:00"
	js.Properties.Set("close", closeTime)
}

// Settings contains optional cafe settings.
type Settings struct {
	// WiFi indicates whether WiFi is available.
	WiFi *bool `json:"wifi,omitempty" jsonschema:"title=WiFi,default=true"`
	// MobileOrdering indicates whether mobile ordering is enabled.
	MobileOrdering *bool `json:"mobile_ordering,omitempty" jsonschema:"title=Mobile Ordering,default=false"`
	// CustomOptions contains additional custom settings.
	CustomOptions map[string]string `json:"custom_options,omitempty" jsonschema:"title=Custom Options"`
	// Theme is the UI theme for digital displays.
	Theme string `json:"theme,omitempty" jsonschema:"title=Theme,enum=light,enum=dark,enum=auto,default=auto"`
}

// ptrUint64 returns a pointer to a uint64 value.
func ptrUint64(v uint64) *uint64 {
	return &v
}
