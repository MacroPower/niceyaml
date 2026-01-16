package theme

import "github.com/macropower/niceyaml/style"

// Theme represents a color theme with its name, mode, and style generator.
type Theme struct {
	Styles func() style.Styles
	Name   string
	Mode   style.Mode
}

var themes = []Theme{
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
	{GithubDark, "github-dark", style.Dark},
	{Gruvbox, "gruvbox", style.Dark},
	{GruvboxLight, "gruvbox-light", style.Light},
	{HrHighContrast, "hr-high-contrast", style.Dark},
	{Hrdark, "hrdark", style.Dark},
	{Igor, "igor", style.Light},
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

// List returns theme names matching the given [style.Mode].
func List(m style.Mode) []string {
	var names []string
	for _, t := range themes {
		if t.Mode == m {
			names = append(names, t.Name)
		}
	}

	return names
}

// Styles returns the [style.Styles] for the given theme name.
func Styles(name string) (style.Styles, bool) {
	for _, t := range themes {
		if t.Name == name {
			return t.Styles(), true
		}
	}

	return style.Styles{}, false
}
