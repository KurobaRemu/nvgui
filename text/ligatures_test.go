package text

import (
	"runtime"
	"strings"
	"testing"
)

func TestLigatureFeaturesNormalizationAndToggle(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "calt=0,liga=0,clig=0,dlig=0"},
		{"normalized", " kern=0 , , -liga, +ss01, zero ", "kern=0,ss01,zero,calt=0,liga=0,clig=0,dlig=0"},
		{"overrides", "kern=0,calt=1,liga=1,clig=1,dlig=1,ss01", "kern=0,ss01,calt=0,liga=0,clig=0,dlig=0"},
		{"already off", "calt=0,liga=0,clig=0,dlig=0,kern=0", "kern=0,calt=0,liga=0,clig=0,dlig=0"},
		{"spaced negative", "-calt = 1,+liga = 1,kern=0", "kern=0,calt=0,liga=0,clig=0,dlig=0"},
		{"spaced positive", "kern=0, -calt = 1, +liga = 1, ss01", "kern=0,ss01,calt=0,liga=0,clig=0,dlig=0"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := DisableLigaturesFeatures(testCase.input); got != testCase.want {
				t.Errorf("DisableLigaturesFeatures(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
			for _, tag := range []string{"calt", "liga", "clig", "dlig"} {
				if count := strings.Count(DisableLigaturesFeatures(testCase.input), tag+"="); count != 1 {
					t.Errorf("DisableLigaturesFeatures(%q) has %d %q tags, want exactly 1", testCase.input, count, tag)
				}
			}
			if got := DisableLigaturesFeatures(testCase.input); got != DisableLigaturesFeatures(got) {
				t.Errorf("DisableLigaturesFeatures is not idempotent: %q -> %q", testCase.input, got)
			}
		})
	}
}

func TestLigatureFeatureFastPathsHaveNoAllocations(t *testing.T) {
	empty := Font{Family: "Menlo", Size: 14, Weight: 400}
	normalized := Font{Family: "Menlo", Size: 14, Weight: 400, Features: "kern=0,ss01,zero"}
	cases := []struct {
		name   string
		invoke func()
	}{
		{name: "empty enabled", invoke: func() {
			if got := WithLigatures(empty, true); got != empty {
				t.Errorf("WithLigatures(empty,true) = %+v, want source font", got)
			}
		}},
		{name: "empty disabled", invoke: func() {
			if got := WithLigatures(empty, false).Features; got != disabledLigatureFeatures {
				t.Errorf("WithLigatures(empty,false).Features = %q, want %q", got, disabledLigatureFeatures)
			}
		}},
		{name: "empty disable helper", invoke: func() {
			if got := DisableLigaturesFeatures(""); got != disabledLigatureFeatures {
				t.Errorf("DisableLigaturesFeatures(\"\") = %q, want %q", got, disabledLigatureFeatures)
			}
		}},
		{name: "normalized enabled", invoke: func() {
			if got := WithLigatures(normalized, true); got != normalized {
				t.Errorf("WithLigatures(normalized,true) = %+v, want source font", got)
			}
		}},
		{name: "normalized positive enabled", invoke: func() {
			positive := Font{Family: "Menlo", Size: 14, Weight: 400, Features: "calt=1,liga=2,ss01"}
			if got := WithLigatures(positive, true); got != positive {
				t.Errorf("WithLigatures(positive,true) = %+v, want source font", got)
			}
		}},
	}
	for _, testCase := range cases {
		allocs := testing.AllocsPerRun(100, testCase.invoke)
		if allocs != 0 {
			t.Errorf("%s allocated %.0f times, want 0", testCase.name, allocs)
		}
	}

	negative := Font{Family: "Menlo", Size: 14, Weight: 400, Features: disabledLigatureFeatures}
	if got := WithLigatures(negative, true); got.Features != "" {
		t.Errorf("WithLigatures(disabled,true).Features = %q, want empty", got.Features)
	}
}

func TestWithLigaturesRestoresWithoutDuplicates(t *testing.T) {
	base := Font{Family: "Menlo", Size: 14, Weight: 400, Italic: true, Features: "kern=0, -liga, +ss01, calt=1"}
	off := WithLigatures(base, false)
	wantOff := "kern=0,ss01,calt=0,liga=0,clig=0,dlig=0"
	if off.Features != wantOff {
		t.Fatalf("WithLigatures(false).Features = %q, want %q", off.Features, wantOff)
	}
	if off.Family != base.Family || off.Size != base.Size || off.Weight != base.Weight || !off.Italic {
		t.Fatalf("WithLigatures(false) changed font fields: %+v", off)
	}
	on := WithLigatures(off, true)
	wantOn := "kern=0,ss01"
	if on.Features != wantOn {
		t.Fatalf("WithLigatures(true).Features = %q, want %q", on.Features, wantOn)
	}
	if base.Features != "kern=0, -liga, +ss01, calt=1" {
		t.Fatalf("WithLigatures mutated the source font: %q", base.Features)
	}
	for _, tag := range ligatureFeatureTags {
		if strings.Contains(on.Features, tag+"=0") {
			t.Errorf("WithLigatures(true) kept %s=0 in %q", tag, on.Features)
		}
	}

	positive := Font{Family: "Menlo", Size: 14, Features: "calt=1,liga=2,clig=3,dlig=4,ss01"}
	got := WithLigatures(positive, true)
	if got.Features != "calt=1,liga=2,clig=3,dlig=4,ss01" {
		t.Errorf("WithLigatures(true) dropped positive ligature switches: %q", got.Features)
	}
	gotOff := WithLigatures(positive, false)
	if strings.Contains(gotOff.Features, "calt=1") || !strings.Contains(gotOff.Features, "ss01") {
		t.Errorf("WithLigatures(false) = %q, want positive switches replaced by off switches", gotOff.Features)
	}
}

func TestWithLigaturesPreservesDarwinKern(t *testing.T) {
	base := Font{Family: "Menlo", Size: 14, Features: "ss01"}
	features := WithLigatures(base, false).MyGo().Features
	if runtime.GOOS == "darwin" && !strings.HasPrefix(features, "kern=0,") {
		t.Errorf("darwin MyGo().Features = %q, want kern=0 prefixed", features)
	}
	for _, tag := range ligatureFeatureTags {
		if !strings.Contains(features, tag+"=0") {
			t.Errorf("MyGo().Features = %q, missing %s=0", features, tag)
		}
	}
	if !strings.Contains(features, "ss01") {
		t.Errorf("MyGo().Features = %q, lost ss01", features)
	}
}
