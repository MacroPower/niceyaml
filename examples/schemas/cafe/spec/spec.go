// Package spec defines the cafe specification schema.
package spec

import (
	"fmt"
	"time"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Spec is the cafe specification.
type Spec struct {
	// SLA is the service level agreement duration for order fulfillment.
	// It defaults to 15 minutes.
	SLA *Duration `json:"sla,omitempty" jsonschema:"title=SLA,pattern=^([0-9]+(\\.[0-9]+)?(ns|[uµμ]s|ms|s|m|h))+$,default=900000000000,examples=900000000000|3600000000000|90000000000"`
	// Settings contains optional cafe settings.
	Settings *Settings `json:"settings,omitempty" jsonschema:"title=Settings"`
	// Hours defines operating hours.
	Hours Hours `json:"hours" jsonschema:"title=Hours"`
	// Menu defines the cafe's menu.
	Menu Menu `json:"menu" jsonschema:"title=Menu"`
	// Staff defines staffing requirements.
	Staff Staff `json:"staff" jsonschema:"title=Staff"`
}

// Menu defines the cafe's menu offerings.
type Menu struct {
	// Items is the list of menu items.
	Items []MenuItem `json:"items" jsonschema:"title=Items,minItems=1"`
}

// MenuItem represents a single item on the menu.
type MenuItem struct {
	// Available indicates whether the item is currently available.
	Available *bool `json:"available,omitempty" jsonschema:"title=Available,default=true"`
	// Name is the name of the menu item.
	Name string `json:"name" jsonschema:"title=Name,minLength=1"`
	// Category is the type of item.
	Category string `json:"category" jsonschema:"title=Category,enum=coffee|tea|pastry|sandwich"`
	// Description provides additional details about the item.
	Description string `json:"description,omitempty" jsonschema:"title=Description"`
	// Tags are optional labels for the item.
	Tags []string `json:"tags,omitempty" jsonschema:"title=Tags"`
	// Price is the cost of the item in dollars.
	Price float64 `json:"price" jsonschema:"title=Price,minimum=0"`
}

// Staff defines staffing requirements.
type Staff struct {
	// Baristas is the number of baristas on shift.
	Baristas int `json:"baristas" jsonschema:"title=Baristas,default=2,minimum=1,maximum=10"`
	// Managers is the number of managers on shift.
	Managers int `json:"managers" jsonschema:"title=Managers,default=1,minimum=1"`
}

// Hours defines operating hours for the cafe.
type Hours struct {
	// Open is the opening time in HH:MM format (24-hour).
	Open string `json:"open" jsonschema:"title=Open,pattern=^([01]?[0-9]|2[0-3]):[0-5][0-9]$,default=07:00"`
	// Close is the closing time in HH:MM format (24-hour).
	Close string `json:"close" jsonschema:"title=Close,pattern=^([01]?[0-9]|2[0-3]):[0-5][0-9]$,default=19:00"`
	// Days lists the days of operation.
	Days []string `json:"days" jsonschema:"title=Days,enum=monday|tuesday|wednesday|thursday|friday|saturday|sunday"`
}

// Validate checks that both times parse and that open is before close.
// A decode calls it wherever a document holds the hours. Validate writes
// each error path from `@`, the hours themselves, so the decode reports
// each problem under the field that holds them, such as $.spec.hours.open.
func (h Hours) Validate() error {
	openTime, err := time.Parse("15:04", h.Open)
	if err != nil {
		return niceyaml.WrapError(
			fmt.Errorf("invalid open time: %w", err),
			niceyaml.AtPath(paths.Current().Child("open")),
		)
	}

	closeTime, err := time.Parse("15:04", h.Close)
	if err != nil {
		return niceyaml.WrapError(
			fmt.Errorf("invalid close time: %w", err),
			niceyaml.AtPath(paths.Current().Child("close")),
		)
	}

	if !openTime.Before(closeTime) {
		return niceyaml.NewError(
			"open must be before close",
			niceyaml.AtPath(paths.Current().Child("open")),
		)
	}

	return nil
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
	Theme string `json:"theme,omitempty" jsonschema:"title=Theme,enum=light|dark|auto,default=auto"`
}

// Duration is a [time.Duration] encoded as text. It parses any
// [time.ParseDuration] form, such as "15m", and marshals with
// [time.Duration.String], so 15 minutes becomes "15m0s". A bare
// [time.Duration] has no encoding/json/v2 representation, so the schema
// generator refuses it. The text form keeps the field a string.
type Duration time.Duration

// MarshalText formats the duration with [time.Duration.String].
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// UnmarshalText parses the duration with [time.ParseDuration]. The schema
// pattern checks the form of a duration but not its range, so a value too
// large for a [time.Duration], such as "99999999999h", passes the schema
// and fails here. A decode binds that failure at the path of the value.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}

	*d = Duration(v)

	return nil
}
