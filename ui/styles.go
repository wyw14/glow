package ui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// Hex values reused by several palette entries.
const (
	defaultGreenHex = "#04B575"
	defaultLogoHex  = "#ECFD65"
)

// Palette keys understood by the style config. Every key is required.
const (
	colorNormalDim         = "normalDim"
	colorGray              = "gray"
	colorMidGray           = "midGray"
	colorDarkGray          = "darkGray"
	colorBrightGray        = "brightGray"
	colorDimBrightGray     = "dimBrightGray"
	colorCream             = "cream"
	colorYellowGreen       = "yellowGreen"
	colorFuchsia           = "fuchsia"
	colorDimFuchsia        = "dimFuchsia"
	colorDullFuchsia       = "dullFuchsia"
	colorDimDullFuchsia    = "dimDullFuchsia"
	colorGreen             = "green"
	colorRed               = "red"
	colorSemiDimGreen      = "semiDimGreen"
	colorDimGreen          = "dimGreen"
	colorMintGreen         = "mintGreen"
	colorDarkGreen         = "darkGreen"
	colorLineNumberFg      = "lineNumberFg"
	colorStatusBarNoteFg   = "statusBarNoteFg"
	colorStatusBarBg       = "statusBarBg"
	colorStatusBarScrollFg = "statusBarScrollPosFg"
	colorTab               = "tab"
	colorSelectedTab       = "selectedTab"
	colorSubtle            = "subtle"
	colorStatusBarHelpBg   = "statusBarHelpBg"
	colorMessageHelpFg     = "messageHelpFg"
	colorHelpBg            = "helpBg"
	colorLogoFg            = "logoFg"
)

// paletteKeys is the complete set of required palette entries, in validation
// order.
var paletteKeys = []string{
	colorNormalDim,
	colorGray,
	colorMidGray,
	colorDarkGray,
	colorBrightGray,
	colorDimBrightGray,
	colorCream,
	colorYellowGreen,
	colorFuchsia,
	colorDimFuchsia,
	colorDullFuchsia,
	colorDimDullFuchsia,
	colorGreen,
	colorRed,
	colorSemiDimGreen,
	colorDimGreen,
	colorMintGreen,
	colorDarkGreen,
	colorLineNumberFg,
	colorStatusBarNoteFg,
	colorStatusBarBg,
	colorStatusBarScrollFg,
	colorTab,
	colorSelectedTab,
	colorSubtle,
	colorStatusBarHelpBg,
	colorMessageHelpFg,
	colorHelpBg,
	colorLogoFg,
}

// AdaptiveColor is a light/dark color pair. The active value is resolved from
// the terminal's background color.
type AdaptiveColor struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// StyleConfig is the declarative palette from which Styles are built. It is
// the input to a style hot-reload transaction.
type StyleConfig struct {
	Colors map[string]AdaptiveColor `json:"colors"`
}

// color returns the adaptive color registered under key.
func (c StyleConfig) color(key string) (AdaptiveColor, error) {
	ac, ok := c.Colors[key]
	if !ok {
		return AdaptiveColor{}, fmt.Errorf("style config: missing required color %q", key)
	}
	return ac, nil
}

// DefaultStyleConfig returns the built-in palette. A fresh map is returned so
// callers can mutate the config without affecting other sessions.
func DefaultStyleConfig() StyleConfig {
	return StyleConfig{Colors: map[string]AdaptiveColor{
		colorNormalDim:         {Light: "#A49FA5", Dark: "#777777"},
		colorGray:              {Light: "#909090", Dark: "#626262"},
		colorMidGray:           {Light: "#B2B2B2", Dark: "#4A4A4A"},
		colorDarkGray:          {Light: "#DDDADA", Dark: "#3C3C3C"},
		colorBrightGray:        {Light: "#847A85", Dark: "#979797"},
		colorDimBrightGray:     {Light: "#C2B8C2", Dark: "#4D4D4D"},
		colorCream:             {Light: "#FFFDF5", Dark: "#FFFDF5"},
		colorYellowGreen:       {Light: defaultGreenHex, Dark: defaultLogoHex},
		colorFuchsia:           {Light: "#EE6FF8", Dark: "#EE6FF8"},
		colorDimFuchsia:        {Light: "#F1A8FF", Dark: "#99519E"},
		colorDullFuchsia:       {Light: "#F793FF", Dark: "#AD58B4"},
		colorDimDullFuchsia:    {Light: "#F6C9FF", Dark: "#7B4380"},
		colorGreen:             {Light: defaultGreenHex, Dark: defaultGreenHex},
		colorRed:               {Light: "#FF4672", Dark: "#ED567A"},
		colorSemiDimGreen:      {Light: "#35D79C", Dark: "#036B46"},
		colorDimGreen:          {Light: "#72D2B0", Dark: "#0B5137"},
		colorMintGreen:         {Light: "#89F0CB", Dark: "#89F0CB"},
		colorDarkGreen:         {Light: "#1C8760", Dark: "#1C8760"},
		colorLineNumberFg:      {Light: "#656565", Dark: "#7D7D7D"},
		colorStatusBarNoteFg:   {Light: "#656565", Dark: "#7D7D7D"},
		colorStatusBarBg:       {Light: "#E6E6E6", Dark: "#242424"},
		colorStatusBarScrollFg: {Light: "#949494", Dark: "#5A5A5A"},
		colorTab:               {Light: "#909090", Dark: "#626262"},
		colorSelectedTab:       {Light: "#333333", Dark: "#979797"},
		colorSubtle:            {Light: "#9B9B9B", Dark: "#5C5C5C"},
		colorStatusBarHelpBg:   {Light: "#DCDCDC", Dark: "#323232"},
		colorMessageHelpFg:     {Light: "#B6FFE4", Dark: "#B6FFE4"},
		colorHelpBg:            {Light: "#f2f2f2", Dark: "#1B1B1B"},
		colorLogoFg:            {Light: defaultLogoHex, Dark: defaultLogoHex},
	}}
}

// Styles contains all styles used in the UI. They are resolved from light and
// dark color variants according to the terminal's background color, which
// Bubble Tea reports via tea.BackgroundColorMsg. Until then, we default to
// a dark background.
type Styles struct {
	lightDark lipgloss.LightDarkFunc

	fuchsia color.Color

	dimNormalFg      func(...string) string
	brightGrayFg     func(...string) string
	dimBrightGrayFg  func(...string) string
	grayFg           func(...string) string
	midGrayFg        func(...string) string
	darkGrayFg       lipgloss.Style
	greenFg          func(...string) string
	semiDimGreenFg   func(...string) string
	dimGreenFg       func(...string) string
	fuchsiaFg        func(...string) string
	dimFuchsiaFg     func(...string) string
	dullFuchsiaFg    func(...string) string
	dimDullFuchsiaFg func(...string) string
	redFg            func(...string) string

	tabStyle         lipgloss.Style
	selectedTabStyle lipgloss.Style
	errorTitleStyle  lipgloss.Style
	subtleStyle      lipgloss.Style
	paginationStyle  lipgloss.Style

	statusBarScrollPosStyle        func(...string) string
	statusBarNoteStyle             func(...string) string
	statusBarHelpStyle             func(...string) string
	statusBarMessageStyle          func(...string) string
	statusBarMessageScrollPosStyle func(...string) string
	statusBarMessageHelpStyle      func(...string) string
	helpViewStyle                  func(...string) string
	lineNumberStyle                func(...string) string

	dividerDot            lipgloss.Style
	dividerBar            lipgloss.Style
	logoStyle             lipgloss.Style
	stashSpinnerStyle     lipgloss.Style
	stashInputPromptStyle lipgloss.Style
}

// newStyles builds all styles for the given terminal background from the
// built-in default palette.
func newStyles(isDark bool) Styles {
	s, err := buildStyles(DefaultStyleConfig(), isDark)
	if err != nil {
		// The compiled-in default palette contains every required key.
		panic(fmt.Sprintf("ui: invalid default style config: %v", err))
	}
	return s
}

// buildStyles resolves the full Styles set from a validated config. Building
// is performed off the active snapshot so a failed reload never mutates the
// styles currently in use.
func buildStyles(cfg StyleConfig, isDark bool) (Styles, error) {
	s := Styles{lightDark: lipgloss.LightDark(isDark)}

	resolve := func(key string) (color.Color, error) {
		ac, err := cfg.color(key)
		if err != nil {
			return nil, err
		}
		return s.lightDark(lipgloss.Color(ac.Light), lipgloss.Color(ac.Dark)), nil
	}

	// Colors
	normalDim, err := resolve(colorNormalDim)
	if err != nil {
		return Styles{}, err
	}
	gray, err := resolve(colorGray)
	if err != nil {
		return Styles{}, err
	}
	midGray, err := resolve(colorMidGray)
	if err != nil {
		return Styles{}, err
	}
	darkGray, err := resolve(colorDarkGray)
	if err != nil {
		return Styles{}, err
	}
	brightGray, err := resolve(colorBrightGray)
	if err != nil {
		return Styles{}, err
	}
	dimBrightGray, err := resolve(colorDimBrightGray)
	if err != nil {
		return Styles{}, err
	}
	cream, err := resolve(colorCream)
	if err != nil {
		return Styles{}, err
	}
	yellowGreen, err := resolve(colorYellowGreen)
	if err != nil {
		return Styles{}, err
	}
	s.fuchsia, err = resolve(colorFuchsia)
	if err != nil {
		return Styles{}, err
	}
	dimFuchsia, err := resolve(colorDimFuchsia)
	if err != nil {
		return Styles{}, err
	}
	dullFuchsia, err := resolve(colorDullFuchsia)
	if err != nil {
		return Styles{}, err
	}
	dimDullFuchsia, err := resolve(colorDimDullFuchsia)
	if err != nil {
		return Styles{}, err
	}
	green, err := resolve(colorGreen)
	if err != nil {
		return Styles{}, err
	}
	red, err := resolve(colorRed)
	if err != nil {
		return Styles{}, err
	}
	semiDimGreen, err := resolve(colorSemiDimGreen)
	if err != nil {
		return Styles{}, err
	}
	dimGreen, err := resolve(colorDimGreen)
	if err != nil {
		return Styles{}, err
	}

	// Pager colors
	mintGreen, err := resolve(colorMintGreen)
	if err != nil {
		return Styles{}, err
	}
	darkGreen, err := resolve(colorDarkGreen)
	if err != nil {
		return Styles{}, err
	}
	lineNumberFg, err := resolve(colorLineNumberFg)
	if err != nil {
		return Styles{}, err
	}
	statusBarNoteFg, err := resolve(colorStatusBarNoteFg)
	if err != nil {
		return Styles{}, err
	}
	statusBarBg, err := resolve(colorStatusBarBg)
	if err != nil {
		return Styles{}, err
	}

	// Render-func styles
	s.dimNormalFg = lipgloss.NewStyle().Foreground(normalDim).Render
	s.brightGrayFg = lipgloss.NewStyle().Foreground(brightGray).Render
	s.dimBrightGrayFg = lipgloss.NewStyle().Foreground(dimBrightGray).Render
	s.grayFg = lipgloss.NewStyle().Foreground(gray).Render
	s.midGrayFg = lipgloss.NewStyle().Foreground(midGray).Render
	s.darkGrayFg = lipgloss.NewStyle().Foreground(darkGray)
	s.greenFg = lipgloss.NewStyle().Foreground(green).Render
	s.semiDimGreenFg = lipgloss.NewStyle().Foreground(semiDimGreen).Render
	s.dimGreenFg = lipgloss.NewStyle().Foreground(dimGreen).Render
	s.fuchsiaFg = lipgloss.NewStyle().Foreground(s.fuchsia).Render
	s.dimFuchsiaFg = lipgloss.NewStyle().Foreground(dimFuchsia).Render
	s.dullFuchsiaFg = lipgloss.NewStyle().Foreground(dullFuchsia).Render
	s.dimDullFuchsiaFg = lipgloss.NewStyle().Foreground(dimDullFuchsia).Render
	s.redFg = lipgloss.NewStyle().Foreground(red).Render

	// Named styles
	tabFg, err := resolve(colorTab)
	if err != nil {
		return Styles{}, err
	}
	selectedTabFg, err := resolve(colorSelectedTab)
	if err != nil {
		return Styles{}, err
	}
	subtleFg, err := resolve(colorSubtle)
	if err != nil {
		return Styles{}, err
	}
	s.tabStyle = lipgloss.NewStyle().Foreground(tabFg)
	s.selectedTabStyle = lipgloss.NewStyle().Foreground(selectedTabFg)
	s.errorTitleStyle = lipgloss.NewStyle().Foreground(cream).Background(red).Padding(0, 1)
	s.subtleStyle = lipgloss.NewStyle().Foreground(subtleFg)
	s.paginationStyle = s.subtleStyle

	// Pager styles
	statusBarScrollFg, err := resolve(colorStatusBarScrollFg)
	if err != nil {
		return Styles{}, err
	}
	statusBarHelpBg, err := resolve(colorStatusBarHelpBg)
	if err != nil {
		return Styles{}, err
	}
	messageHelpFg, err := resolve(colorMessageHelpFg)
	if err != nil {
		return Styles{}, err
	}
	helpBg, err := resolve(colorHelpBg)
	if err != nil {
		return Styles{}, err
	}

	s.statusBarScrollPosStyle = lipgloss.NewStyle().
		Foreground(statusBarScrollFg).
		Background(statusBarBg).
		Render

	s.statusBarNoteStyle = lipgloss.NewStyle().
		Foreground(statusBarNoteFg).
		Background(statusBarBg).
		Render

	s.statusBarHelpStyle = lipgloss.NewStyle().
		Foreground(statusBarNoteFg).
		Background(statusBarHelpBg).
		Render

	s.statusBarMessageStyle = lipgloss.NewStyle().
		Foreground(mintGreen).
		Background(darkGreen).
		Render

	s.statusBarMessageScrollPosStyle = lipgloss.NewStyle().
		Foreground(mintGreen).
		Background(darkGreen).
		Render

	s.statusBarMessageHelpStyle = lipgloss.NewStyle().
		Foreground(messageHelpFg).
		Background(green).
		Render

	s.helpViewStyle = lipgloss.NewStyle().
		Foreground(statusBarNoteFg).
		Background(helpBg).
		Render

	s.lineNumberStyle = lipgloss.NewStyle().
		Foreground(lineNumberFg).
		Render

	// Stash styles
	s.dividerDot = s.darkGrayFg.SetString(" • ")
	s.dividerBar = s.darkGrayFg.SetString(" │ ")

	logoFg, err := resolve(colorLogoFg)
	if err != nil {
		return Styles{}, err
	}
	s.logoStyle = lipgloss.NewStyle().
		Foreground(logoFg).
		Background(s.fuchsia).
		Bold(true)

	s.stashSpinnerStyle = lipgloss.NewStyle().
		Foreground(gray)
	s.stashInputPromptStyle = lipgloss.NewStyle().
		Foreground(yellowGreen).
		MarginRight(1)

	return s, nil
}

// adaptive returns a color appropriate for the current terminal background.
func (s Styles) adaptive(light, dark string) color.Color {
	return s.lightDark(lipgloss.Color(light), lipgloss.Color(dark))
}

// smokeRender exercises every resolved style so a broken configuration fails
// the transaction before it is published rather than panicking mid-frame.
func (s Styles) smokeRender() error {
	renderFuncs := []func(...string) string{
		s.dimNormalFg,
		s.brightGrayFg,
		s.dimBrightGrayFg,
		s.grayFg,
		s.midGrayFg,
		s.greenFg,
		s.semiDimGreenFg,
		s.dimGreenFg,
		s.fuchsiaFg,
		s.dimFuchsiaFg,
		s.dullFuchsiaFg,
		s.dimDullFuchsiaFg,
		s.redFg,
		s.statusBarScrollPosStyle,
		s.statusBarNoteStyle,
		s.statusBarHelpStyle,
		s.statusBarMessageStyle,
		s.statusBarMessageScrollPosStyle,
		s.statusBarMessageHelpStyle,
		s.helpViewStyle,
		s.lineNumberStyle,
	}
	for i, f := range renderFuncs {
		if f == nil {
			return fmt.Errorf("style config: render function %d was not initialized", i)
		}
		_ = f("g")
	}

	namedStyles := []lipgloss.Style{
		s.darkGrayFg,
		s.tabStyle,
		s.selectedTabStyle,
		s.errorTitleStyle,
		s.subtleStyle,
		s.paginationStyle,
		s.dividerDot,
		s.dividerBar,
		s.logoStyle,
		s.stashSpinnerStyle,
		s.stashInputPromptStyle,
	}
	for _, st := range namedStyles {
		_ = st.Render("g")
	}
	return nil
}
