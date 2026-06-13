// Package spec defines the cafe specification schema.
package spec

import "time"

// Spec is the cafe specification.
type Spec struct {
	// SLA is the service level agreement duration for order fulfillment.
	// Defaults to 15 minutes.
	SLA *time.Duration `json:"sla,omitempty" jsonschema:"title=SLA,type=string,pattern=^(\\d+d)?(\\d+h)?(\\d+m)?(\\d+s)?$,default=15m,examples=15m|1h|90s"`
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
