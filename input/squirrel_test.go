package input

import (
	"strings"
	"testing"
)

func TestParseSquirrelAppOptionsForms(t *testing.T) {
	data := []byte(`
# squirrel.yaml
style:
  color_scheme: native

app_options:
  com.apple.Terminal:
    ascii_mode: true # inline comment
    no_inline: false
  "com.example.nvgui":
    vim_mode: true
  'com.googlecode.iterm2':
    ascii_mode: false

patch:
  app_options:
    com.example.patch:
      ascii_mode: true
  app_options/org.vim.MacVim:
    vim_mode: false
    no_inline: true
  app_options.com.example.dotted:
    ascii_mode: false
`)
	options, err := ParseSquirrelAppOptions(data)
	if err != nil {
		t.Fatalf("ParseSquirrelAppOptions: %v", err)
	}
	if len(options) != 6 {
		t.Fatalf("options len = %d, want 6: %+v", len(options), options)
	}
	assertSquirrelBool(t, options["com.apple.Terminal"].ASCIIMode, true)
	assertSquirrelBool(t, options["com.apple.Terminal"].NoInline, false)
	if options["com.apple.Terminal"].VIMMode != nil {
		t.Error("omitted VIMMode is not nil")
	}
	assertSquirrelBool(t, options["com.example.nvgui"].VIMMode, true)
	assertSquirrelBool(t, options["com.googlecode.iterm2"].ASCIIMode, false)
	assertSquirrelBool(t, options["com.example.patch"].ASCIIMode, true)
	assertSquirrelBool(t, options["org.vim.MacVim"].VIMMode, false)
	assertSquirrelBool(t, options["org.vim.MacVim"].NoInline, true)
	assertSquirrelBool(t, options["com.example.dotted"].ASCIIMode, false)
	for name, option := range options {
		if option.BundleID != name {
			t.Errorf("options[%q].BundleID = %q", name, option.BundleID)
		}
	}
}

func TestParseSquirrelAppOptionsRealConfig(t *testing.T) {
	data := []byte(`config_version: '1.0'
us_keyboard_layout: false
show_notifications_when: growl_is_running

preset_color_schemes:
  native:
    name: 原生／Native
    author: Rime
    back_color: 0xFFFFFF
    desc: "a: b # not a comment"
 # This tab	stays inside a comment and must not break parsing.
 # leading single space comment
style:
  color_scheme: native

app_options:
  com.example.nvgui:
   ascii_mode: true
   vim_mode: false
   no_inline: true

patch:
  app_options/com.example.patch:
    ascii_mode: false
`)
	options, err := ParseSquirrelAppOptions(data)
	if err != nil {
		t.Fatalf("ParseSquirrelAppOptions: %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("options len = %d, want 2: %+v", len(options), options)
	}
	assertSquirrelBool(t, options["com.example.nvgui"].ASCIIMode, true)
	assertSquirrelBool(t, options["com.example.nvgui"].VIMMode, false)
	assertSquirrelBool(t, options["com.example.nvgui"].NoInline, true)
	assertSquirrelBool(t, options["com.example.patch"].ASCIIMode, false)
}

func TestParseSquirrelAppOptionsEmptyAndMalformed(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":          {},
		"comments_only":  []byte("# comment\n\n"),
		"no_app_options": []byte("style:\n  color_scheme: native\n"),
	} {
		options, err := ParseSquirrelAppOptions(data)
		if err != nil || len(options) != 0 {
			t.Errorf("%s: options = %+v, err = %v, want empty map and nil error", name, options, err)
		}
	}
	for name, data := range map[string][]byte{
		"non_bool_ascii_mode": []byte("app_options:\n  com.example.nvgui:\n    ascii_mode: maybe\n"),
		"tab_indentation":     []byte("app_options:\n\tcom.example.nvgui:\n\t\tascii_mode: true\n"),
		"unterminated_quote":  []byte("app_options:\n  \"com.example.nvgui:\n    ascii_mode: true\n"),
		"unknown_option":      []byte("app_options:\n  com.example.nvgui:\n    unknown: true\n"),
		"missing_bundle_id":   []byte("app_options:\n  ascii_mode: true\n"),
		"missing_colon":       []byte("app_options:\n  com.example.nvgui:\n    ascii_mode true\n"),
	} {
		if _, err := ParseSquirrelAppOptions(data); err == nil {
			t.Errorf("%s returned nil error, want error", name)
		}
	}
}

func TestSplitSquirrelPairQuotedColonAndEscapes(t *testing.T) {
	key, value, hasColon, err := splitSquirrelPair(`"com.example:nvgui": desc: "a: b # kept \" literal"`, 7)
	if err != nil {
		t.Fatalf("splitSquirrelPair: %v", err)
	}
	if !hasColon || key != "com.example:nvgui" || value != `desc: "a: b # kept \" literal"` {
		t.Fatalf("splitSquirrelPair = (%q, %q, %v), want quoted key and value with inner colon", key, value, hasColon)
	}
	if _, _, _, err := splitSquirrelPair(`"invalid`, 8); err == nil {
		t.Fatal("splitSquirrelPair accepted unterminated quote")
	}
	if _, err := unquoteSquirrel(`"bad\q"`); err == nil {
		t.Fatal("unquoteSquirrel silently accepted invalid escape")
	}
	if got, err := stripSquirrelComment(`key: "a#b\"#c" # comment`); err != nil || got != `key: "a#b\"#c" ` {
		t.Fatalf("stripSquirrelComment = (%q, %v), want quoted hash and escaped quote preserved", got, err)
	}
	if got, err := stripSquirrelComment(` # leading single-space comment`); err != nil || got != ` ` {
		t.Fatalf("stripSquirrelComment(comment) = (%q, %v), want leading whitespace only", got, err)
	}
}

func TestCheckSquirrelBundleCLIMismatch(t *testing.T) {
	options := map[string]SquirrelAppOption{
		"com.example.nvgui": {BundleID: "com.example.nvgui"},
	}
	matched := CheckSquirrelBundle("com.example.nvgui", "/usr/local/bin/nvgui", options)
	if !matched.Matched || matched.CLIMismatch {
		t.Fatalf("matched CLI = %+v, want Matched=true CLIMismatch=false", matched)
	}
	emptyBundle := CheckSquirrelBundle("", "/Applications/nvgui.app/Contents/MacOS/nvgui", options)
	if !emptyBundle.CLIMismatch || strings.TrimSpace(emptyBundle.Warning) == "" {
		t.Fatalf("empty bundleID = %+v, want CLIMismatch=true with warning", emptyBundle)
	}
	unmatchedCLI := CheckSquirrelBundle("com.example.other", "/usr/local/bin/nvgui", options)
	if !unmatchedCLI.CLIMismatch || strings.TrimSpace(unmatchedCLI.Warning) == "" {
		t.Fatalf("unmatched CLI = %+v, want CLIMismatch=true with warning", unmatchedCLI)
	}
	unmatchedApp := CheckSquirrelBundle("com.example.other", "/Applications/nvgui.app/Contents/MacOS/nvgui", options)
	if unmatchedApp.Matched || unmatchedApp.CLIMismatch {
		t.Fatalf("unmatched .app = %+v, want Matched=false CLIMismatch=false", unmatchedApp)
	}
	options[""] = SquirrelAppOption{BundleID: "", ASCIIMode: boolPointer(true)}
	emptyKeyMatch := CheckSquirrelBundle("", "/Applications/nvgui.app/Contents/MacOS/nvgui", options)
	if emptyKeyMatch.Matched || !emptyKeyMatch.CLIMismatch || emptyKeyMatch.Option.BundleID != "" {
		t.Fatalf("empty bundleID matched opts[\"\"] = %+v, want unmatched", emptyKeyMatch)
	}
}

func boolPointer(value bool) *bool { return &value }

func assertSquirrelBool(t *testing.T, got *bool, want bool) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("bool option = %v, want %v", got, want)
	}
}
