package styleplan

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

const (
	testTerm256     = "TERM=xterm-256color"
	testTermXterm   = "TERM=xterm"
	testTermDumb    = "TERM=dumb"
	testTrueColor   = "COLORTERM=truecolor"
	testTTYForce    = "TTY_FORCE=1"
	testNoColor     = "NO_COLOR=1"
	testKittyWindow = "KITTY_WINDOW_ID=1"
)

func envWith(overrides ...string) []string {
	return overrides
}

func TestDetectRedirectedOutput(t *testing.T) {
	sink := &bytes.Buffer{}
	caps := Detect(sink, envWith(testTerm256, testTrueColor, testKittyWindow))

	if caps.Interactive {
		t.Error("expected redirected output to be non-interactive")
	}
	if caps.Color != colorprofile.NoTTY || !caps.ColorKnown {
		t.Errorf("expected known NoTTY profile, got %s (known=%v)", caps.Color, caps.ColorKnown)
	}
	if caps.Unicode != UnicodeASCII {
		t.Errorf("expected ASCII Unicode level for redirected output, got %s", caps.Unicode)
	}
	if caps.WidthKnown {
		t.Error("redirected output must not report a known terminal width")
	}
	if caps.TextSizing {
		t.Error("text sizing must stay disabled for redirected output")
	}
	if sink.Len() != 0 {
		t.Errorf("planning must not write to the output, got %q", sink.String())
	}
}

func TestDetectInteractiveColorDepth(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want colorprofile.Profile
	}{
		{"256 color", envWith(testTTYForce, testTerm256), colorprofile.ANSI256},
		{"true color", envWith(testTTYForce, testTerm256, testTrueColor), colorprofile.TrueColor},
		{"no color", envWith(testTTYForce, testTermXterm, testNoColor), colorprofile.ASCII},
		{"dumb terminal", envWith(testTTYForce, testTermDumb), colorprofile.NoTTY},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := Detect(&bytes.Buffer{}, tt.env)
			if !caps.Interactive {
				t.Fatal("expected TTY_FORCE to mark the output interactive")
			}
			if caps.Color != tt.want {
				t.Errorf("expected %s, got %s", tt.want, caps.Color)
			}
			if !caps.ColorKnown {
				t.Error("interactive detection should resolve the color profile decisively")
			}
		})
	}
}

func TestDetectUnicodeLevel(t *testing.T) {
	if caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm)); caps.Unicode != UnicodeFull {
		t.Errorf("expected full Unicode on xterm, got %s", caps.Unicode)
	}
	if caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm, "GLOW_UNICODE=ascii")); caps.Unicode != UnicodeASCII {
		t.Errorf("expected GLOW_UNICODE=ascii to force ASCII, got %s", caps.Unicode)
	}
	if caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermDumb, "GLOW_UNICODE=unicode")); caps.Unicode != UnicodeFull {
		t.Errorf("expected GLOW_UNICODE=unicode to override the dumb terminal, got %s", caps.Unicode)
	}
	if caps := Detect(&bytes.Buffer{}, envWith(testTermXterm)); caps.Unicode != UnicodeASCII {
		t.Errorf("expected redirected output to default to ASCII, got %s", caps.Unicode)
	}
}

func TestDetectTextSizing(t *testing.T) {
	if caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm, testKittyWindow)); !caps.TextSizing {
		t.Error("expected kitty environment to enable text sizing")
	}
	if caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm, testKittyWindow, "GLOW_TEXT_SIZING=off")); caps.TextSizing {
		t.Error("expected GLOW_TEXT_SIZING=off to disable text sizing")
	}
	if caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm, "GLOW_TEXT_SIZING=on")); !caps.TextSizing {
		t.Error("expected GLOW_TEXT_SIZING=on to enable text sizing")
	}
}

func TestDetectEastAsianLocale(t *testing.T) {
	tests := []struct {
		locale string
		want   bool
	}{
		{"ja_JP.UTF-8", true},
		{"zh_CN.UTF-8", true},
		{"ko_KR.UTF-8", true},
		{"en_US.UTF-8", false},
		{"C", false},
		{"POSIX", false},
		{"ja_JP.SJIS", true},
		{"de_DE.UTF-8", false},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			caps := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm, "LANG="+tt.locale))
			if caps.EastAsianWidth != tt.want {
				t.Errorf("locale %q: expected EastAsianWidth=%v, got %v", tt.locale, tt.want, caps.EastAsianWidth)
			}
		})
	}
}

func TestDetectVersionFollowsEnvironment(t *testing.T) {
	base := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm))
	upgraded := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTerm256))
	repeated := Detect(&bytes.Buffer{}, envWith(testTTYForce, testTermXterm))

	if base.Version() == upgraded.Version() {
		t.Error("a color depth change must produce a new version")
	}
	if base.Version() != repeated.Version() {
		t.Error("identical environments must produce identical versions")
	}
	if upgraded.Version().Color != colorprofile.ANSI256 {
		t.Errorf("expected the upgraded version to carry ANSI256, got %s", upgraded.Version().Color)
	}
}

func TestDetectNeverWrites(t *testing.T) {
	sink := &bytes.Buffer{}
	_ = Detect(sink, envWith(testTTYForce, testTerm256, testTrueColor, testKittyWindow))
	if sink.Len() != 0 {
		t.Fatalf("detection wrote %d bytes to the output target", sink.Len())
	}
}
