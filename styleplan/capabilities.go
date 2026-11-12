// Package styleplan detects terminal capabilities and plans immutable
// rendering strategies together with the fields that must be degraded.
//
// Detection and planning are pure operations: they never write to the
// terminal, never query it with escape sequences and never mutate the
// user selected theme. Capabilities are captured as comparable snapshots
// whose Version drives cache invalidation.
package styleplan

import (
	"io"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// UnicodeLevel describes how much of Unicode the output target can render.
type UnicodeLevel uint8

const (
	// UnicodeUnknown means Unicode support could not be determined.
	UnicodeUnknown UnicodeLevel = iota
	// UnicodeASCII restricts output to ASCII-only glyphs.
	UnicodeASCII
	// UnicodeFull allows box drawing, symbols and wide glyphs.
	UnicodeFull
)

// String returns the string representation of the Unicode level.
func (u UnicodeLevel) String() string {
	switch u {
	case UnicodeUnknown:
		return "unknown"
	case UnicodeASCII:
		return "ascii"
	case UnicodeFull:
		return "unicode"
	default:
		return "unknown"
	}
}

// Capabilities is an immutable snapshot of everything the planner needs to
// know about an output target. Instances are values, never modified after
// Detect returns them.
type Capabilities struct {
	// Interactive reports whether the output is an interactive terminal.
	// It is false for redirected or piped output.
	Interactive bool

	// Color is the detectable color profile of the output target.
	Color colorprofile.Profile
	// ColorKnown reports whether Color was detected decisively. When false
	// the planner falls back to a conservative, colorless strategy.
	ColorKnown bool

	// Unicode is the usable Unicode rendering level.
	Unicode UnicodeLevel

	// EastAsianWidth reports whether East Asian ambiguous width characters
	// must be treated as double width according to the active locale.
	EastAsianWidth bool

	// Width is the terminal width in cells when WidthKnown is true.
	Width int
	// WidthKnown reports whether Width was obtained from the terminal.
	WidthKnown bool

	// TextSizing reports whether kitty text sizing (OSC 66) may be used.
	// It is derived from environment signals only, never from a probe.
	TextSizing bool

	// envSignature summarizes the environment values that influenced the
	// detection so that an environment change yields a new Version.
	envSignature string
}

// Version is the comparable identity of a Capabilities snapshot. Caches use
// it as an invalidation key: any capability change produces a new version.
type Version struct {
	Interactive    bool
	Color          colorprofile.Profile
	ColorKnown     bool
	Unicode        UnicodeLevel
	EastAsianWidth bool
	Width          int
	WidthKnown     bool
	TextSizing     bool
	EnvSignature   string
}

// Version returns the comparable version of the capability snapshot.
func (c Capabilities) Version() Version {
	return Version{
		Interactive:    c.Interactive,
		Color:          c.Color,
		ColorKnown:     c.ColorKnown,
		Unicode:        c.Unicode,
		EastAsianWidth: c.EastAsianWidth,
		Width:          c.Width,
		WidthKnown:     c.WidthKnown,
		TextSizing:     c.TextSizing,
		EnvSignature:   c.envSignature,
	}
}

// envMap is a lookup-only view over an os.Environ style slice.
type envMap map[string]string

func parseEnviron(environ []string) envMap {
	m := make(envMap, len(environ))
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		if ok {
			m[key] = value
		}
	}
	return m
}

// signatureKeys lists every environment variable that can influence Detect.
// The signature is order independent and only changes when one of them does.
var signatureKeys = []string{
	"CLICOLOR",
	"CLICOLOR_FORCE",
	"COLORTERM",
	"GLOW_TEXT_SIZING",
	"GLOW_UNICODE",
	"GOOGLE_CLOUD_SHELL",
	"KITTY_WINDOW_ID",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"NO_COLOR",
	"TERM",
	"TERM_PROGRAM",
	"TMUX",
	"TTY_FORCE",
	"WT_SESSION",
}

func (e envMap) signature() string {
	var b strings.Builder
	for _, key := range signatureKeys {
		value, ok := e[key]
		if !ok {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte(0)
	}
	return b.String()
}

func boolEnv(env envMap, key string) bool {
	parsed, err := strconv.ParseBool(env[key])
	return err == nil && parsed
}

// fdHolder is implemented by *os.File and any writer exposing a descriptor.
type fdHolder interface {
	Fd() uintptr
}

// Detect inspects the given writer and environment without performing any
// terminal input or output. Redirected writers, missing descriptors and
// unreadable terminal sizes are reported as unknown instead of probed.
func Detect(w io.Writer, environ []string) Capabilities {
	env := parseEnviron(environ)
	fd, hasFD := terminalFD(w)
	interactive := boolEnv(env, "TTY_FORCE") || hasFD && term.IsTerminal(int(fd))

	caps := Capabilities{
		Interactive:    interactive,
		Color:          detectColor(interactive, environ),
		ColorKnown:     true,
		Unicode:        detectUnicode(interactive, env),
		EastAsianWidth: eastAsianLocale(env),
		TextSizing:     detectTextSizing(interactive, env),
		envSignature:   env.signature(),
	}
	if caps.Color == colorprofile.Unknown {
		// The environment does not describe any color capability; choose the
		// conservative fallback and remember that it was not confirmed.
		caps.Color = colorprofile.ASCII
		caps.ColorKnown = false
	}
	caps.Width, caps.WidthKnown = detectWidth(interactive, hasFD, fd)
	return caps
}

func terminalFD(w io.Writer) (uintptr, bool) {
	f, ok := w.(fdHolder)
	if !ok {
		return 0, false
	}
	return f.Fd(), true
}

// detectColor resolves the color depth from environment signals only.
// Redirected output is a known NoTTY state rather than an unknown one.
func detectColor(interactive bool, environ []string) colorprofile.Profile {
	if !interactive {
		return colorprofile.NoTTY
	}
	return colorprofile.Env(environ)
}

// detectUnicode resolves the usable Unicode level without probing the
// terminal. An explicit GLOW_UNICODE override wins over auto detection.
func detectUnicode(interactive bool, env envMap) UnicodeLevel {
	switch strings.ToLower(strings.TrimSpace(env["GLOW_UNICODE"])) {
	case "ascii":
		return UnicodeASCII
	case "unicode", "full", "utf8", "utf-8":
		return UnicodeFull
	}

	if !interactive {
		// Plain files and pipes must stay safe for downstream consumers.
		return UnicodeASCII
	}
	switch term := env["TERM"]; {
	case term == "dumb":
		return UnicodeASCII
	case term == "" && runtime.GOOS != "windows" && !boolEnv(env, "TTY_FORCE"):
		return UnicodeUnknown
	default:
		return UnicodeFull
	}
}

// detectWidth reads the terminal size through an ioctl. It never falls back
// to a default width: unknown widths are left to the planner to decide.
func detectWidth(interactive, hasFD bool, fd uintptr) (int, bool) {
	if !interactive || !hasFD {
		return 0, false
	}
	width, _, err := term.GetSize(int(fd))
	if err != nil || width <= 0 {
		return 0, false
	}
	return width, true
}

// detectTextSizing mirrors the environment-based portion of the OSC 66
// detection. The interactive /dev/tty probe is intentionally excluded so
// planning stays side-effect free.
func detectTextSizing(interactive bool, env envMap) bool {
	switch strings.ToLower(strings.TrimSpace(env["GLOW_TEXT_SIZING"])) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}

	if !interactive {
		return false
	}
	return env["KITTY_WINDOW_ID"] != "" ||
		env["TERM_PROGRAM"] == "kitty" ||
		strings.Contains(env["TERM"], "kitty")
}

var localeCharset = regexp.MustCompile(`^[a-z][a-z][a-z]?(?:_[A-Z][A-Z])?\.(.+)`)

// multibyteCharsets mirrors the charset table used by go-runewidth so the
// East Asian ambiguous width decision matches StringWidth at render time.
var multibyteCharsets = map[string]bool{
	"big5":    true,
	"cp932":   true,
	"cp936":   true,
	"cp949":   true,
	"cp950":   true,
	"eucjp":   true,
	"euccn":   true,
	"euckr":   true,
	"gb2312":  true,
	"gbk":     true,
	"jis":     true,
	"sjis":    true,
	"utf-8":   true,
	"utf8":    true,
	"cp51932": true,
}

// eastAsianLocale determines ambiguous width handling from locale variables
// in the supplied environment instead of process-global state.
func eastAsianLocale(env envMap) bool {
	locale := firstNonEmpty(env["LC_ALL"], env["LC_CTYPE"], env["LANG"])
	if locale == "POSIX" || locale == "C" {
		return false
	}
	if len(locale) > 1 && locale[0] == 'C' && (locale[1] == '.' || locale[1] == '-') {
		return false
	}

	charset := strings.ToLower(locale)
	if match := localeCharset.FindStringSubmatch(locale); len(match) == 2 {
		charset = strings.ToLower(match[1])
	}
	if strings.HasSuffix(charset, "@cjk_narrow") {
		return false
	}
	if at := strings.IndexByte(charset, '@'); at >= 0 {
		charset = charset[:at]
	}
	if !multibyteCharsets[charset] {
		return false
	}
	// UTF-8 is only treated as East Asian for CJK locales, matching
	// go-runewidth; legacy multibyte charsets imply it on their own.
	if strings.HasPrefix(charset, "u") {
		return strings.HasPrefix(locale, "ja") ||
			strings.HasPrefix(locale, "ko") ||
			strings.HasPrefix(locale, "zh")
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
