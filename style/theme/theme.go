package theme

import (
	"sync"

	"go.jacobcolvin.com/niceyaml/style"
)

// Theme is a registry entry: a named color theme with its mode and style
// generator.
//
// Look one up by name with [Get], enumerate them with [All], or use the
// [Styles] and [List] shortcuts when only the styles or the names are needed.
type Theme struct {
	// Styles returns the [style.Styles] for this theme.
	Styles func() style.Styles
	// Name is the kebab-case identifier for the theme (e.g., "monokai", "dracula").
	Name string
	// Mode indicates whether the theme is designed for light or dark backgrounds.
	Mode style.Mode
}

var (
	customMu     sync.RWMutex
	customThemes = map[string]Theme{}
	// Names in registration order, so All is deterministic.
	customOrder []string

	// Indexes the built-in themes by name.
	builtinIndex = func() map[string]Theme {
		m := make(map[string]Theme, len(themes))
		for _, t := range themes {
			m[t.Name] = t
		}

		return m
	}()

	themes = []Theme{
		{Abap, "abap", style.Light},
		{Algol, "algol", style.Light},
		{AlgolNu, "algol-nu", style.Light},
		{Arduino, "arduino", style.Light},
		{Ashen, "ashen", style.Dark},
		{AuraThemeDark, "aura-theme-dark", style.Dark},
		{AuraThemeDarkSoft, "aura-theme-dark-soft", style.Dark},
		{Autumn, "autumn", style.Light},
		{Average, "average", style.Dark},
		{Base16Snazzy, "base16-snazzy", style.Dark},
		{Borland, "borland", style.Light},
		{Bw, "bw", style.Light},
		{CatppuccinFrappe, "catppuccin-frappe", style.Dark},
		{CatppuccinLatte, "catppuccin-latte", style.Light},
		{CatppuccinMacchiato, "catppuccin-macchiato", style.Dark},
		{CatppuccinMocha, "catppuccin-mocha", style.Dark},
		{Charm, "charm", style.Dark},
		{Colorful, "colorful", style.Light},
		{DoomOne, "doom-one", style.Dark},
		{DoomOne2, "doom-one2", style.Dark},
		{Dracula, "dracula", style.Dark},
		{Emacs, "emacs", style.Light},
		{Evergarden, "evergarden", style.Dark},
		{Friendly, "friendly", style.Light},
		{Fruity, "fruity", style.Dark},
		{Github, "github", style.Light},
		{GithubDark, "github-dark", style.Dark},
		{Gruvbox, "gruvbox", style.Dark},
		{GruvboxLight, "gruvbox-light", style.Light},
		{HrHighContrast, "hr-high-contrast", style.Dark},
		{Hrdark, "hrdark", style.Dark},
		{Igor, "igor", style.Light},
		{KanagawaDragon, "kanagawa-dragon", style.Dark},
		{KanagawaLotus, "kanagawa-lotus", style.Light},
		{KanagawaWave, "kanagawa-wave", style.Dark},
		{Lovelace, "lovelace", style.Light},
		{Manni, "manni", style.Light},
		{ModusOperandi, "modus-operandi", style.Light},
		{ModusVivendi, "modus-vivendi", style.Dark},
		{Monokai, "monokai", style.Dark},
		{Monokailight, "monokailight", style.Light},
		{Murphy, "murphy", style.Light},
		{Native, "native", style.Dark},
		{Nord, "nord", style.Dark},
		{Nordic, "nordic", style.Dark},
		{Onedark, "onedark", style.Dark},
		{Onesenterprise, "onesenterprise", style.Light},
		{ParaisoDark, "paraiso-dark", style.Dark},
		{ParaisoLight, "paraiso-light", style.Light},
		{Pastie, "pastie", style.Light},
		{Perldoc, "perldoc", style.Light},
		{Pygments, "pygments", style.Light},
		{RainbowDash, "rainbow-dash", style.Light},
		{RosePine, "rose-pine", style.Dark},
		{RosePineDawn, "rose-pine-dawn", style.Light},
		{RosePineMoon, "rose-pine-moon", style.Dark},
		{Rpgle, "rpgle", style.Light},
		{Rrt, "rrt", style.Dark},
		{SolarizedDark, "solarized-dark", style.Dark},
		{SolarizedDark256, "solarized-dark256", style.Dark},
		{SolarizedLight, "solarized-light", style.Light},
		{Swapoff, "swapoff", style.Dark},
		{Tango, "tango", style.Light},
		{TokyonightDay, "tokyonight-day", style.Light},
		{TokyonightMoon, "tokyonight-moon", style.Dark},
		{TokyonightNight, "tokyonight-night", style.Dark},
		{TokyonightStorm, "tokyonight-storm", style.Dark},
		{Trac, "trac", style.Light},
		{Vim, "vim", style.Dark},
		{Vs, "vs", style.Light},
		{Vulcan, "vulcan", style.Dark},
		{Witchhazel, "witchhazel", style.Dark},
		{Xcode, "xcode", style.Light},
		{XcodeDark, "xcode-dark", style.Dark},
	}
)

// Register registers a custom theme by name.
//
// Registered themes become available through [Styles] and [List].
// If a theme with the same name already exists (built-in or custom),
// it is replaced.
//
// Register is safe for concurrent use.
func Register(name string, fn func() style.Styles, mode style.Mode) {
	customMu.Lock()
	defer customMu.Unlock()

	if _, exists := customThemes[name]; !exists {
		customOrder = append(customOrder, name)
	}

	customThemes[name] = Theme{Styles: fn, Name: name, Mode: mode}
}

// Get returns the [Theme] registered under name. The boolean reports whether
// one was found.
//
// Custom themes registered with [Register] take precedence over built-in
// themes with the same name.
func Get(name string) (Theme, bool) {
	if t, ok := lookupCustom(name); ok {
		return t, true
	}

	t, ok := builtinIndex[name]

	return t, ok
}

// lookupCustom returns the custom theme registered under name.
func lookupCustom(name string) (Theme, bool) {
	customMu.RLock()
	defer customMu.RUnlock()

	t, ok := customThemes[name]

	return t, ok
}

// All returns every registered [Theme], built-in ones first in alphabetical
// order followed by custom ones in registration order. A custom theme that
// shares a built-in name replaces it in the result.
func All() []Theme {
	customMu.RLock()
	defer customMu.RUnlock()

	result := make([]Theme, 0, len(themes)+len(customThemes))

	for _, t := range themes {
		if custom, ok := customThemes[t.Name]; ok {
			result = append(result, custom)

			continue
		}

		result = append(result, t)
	}

	for _, name := range customOrder {
		if _, builtin := builtinIndex[name]; builtin {
			continue
		}

		result = append(result, customThemes[name])
	}

	return result
}

// List returns the names of all themes matching the given [style.Mode], in
// the order of [All].
//
// Names are kebab-case identifiers (e.g., "monokai", "catppuccin-mocha")
// suitable for passing to [Styles] or [Get].
func List(m style.Mode) []string {
	var names []string

	for _, t := range All() {
		if t.Mode == m {
			names = append(names, t.Name)
		}
	}

	return names
}

// Styles returns the [style.Styles] for the named theme.
// The boolean reports whether the theme was found.
//
// Styles is shorthand for [Get] followed by calling [Theme.Styles].
func Styles(name string) (style.Styles, bool) {
	t, ok := Get(name)
	if !ok {
		return style.Styles{}, false
	}

	return t.Styles(), true
}
