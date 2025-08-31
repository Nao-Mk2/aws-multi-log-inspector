package cmd

import (
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// helper to temporarily set env var
func withEnv(key, val string, fn func()) {
	old, had := os.LookupEnv(key)
	_ = os.Setenv(key, val)
	defer func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}()
	fn()
}

// helper to temporarily unset env var
func withoutEnv(key string, fn func()) {
	old, had := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	defer func() {
		if had {
			_ = os.Setenv(key, old)
		}
	}()
	fn()
}

// helper to run with a fresh FlagSet and custom os.Args
func withFlagSet(args []string, fn func()) {
	oldCmd := flag.CommandLine
	oldArgs := os.Args
	defer func() {
		flag.CommandLine = oldCmd
		os.Args = oldArgs
	}()
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flag.CommandLine = fs
	os.Args = args
	fn()
}

func TestParseGroupsCSV(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"simple", "a,b,c", []string{"a", "b", "c"}},
		{"spaces", " a, b ,c ", []string{"a", "b", "c"}},
		{"empties", ",a,,b,", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseGroupsCSV(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseGroupsCSV(%q)=%v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveProfile(t *testing.T) {
	tests := []struct {
		name        string
		flagProfile string
		envProfile  string
		want        string
	}{
		{"flag-wins", "flagP", "envP", "flagP"},
		{"env-only", "", "envP", "envP"},
		{"none", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withEnv("AWS_PROFILE", tt.envProfile, func() {
				got := ResolveProfile(tt.flagProfile)
				if got != tt.want {
					t.Fatalf("ResolveProfile(%q)=%q, want %q", tt.flagProfile, got, tt.want)
				}
			})
		})
	}
}

func TestDefaultTimeWindow(t *testing.T) {
	start, end := DefaultTimeWindow()
	if !end.After(start) {
		t.Fatalf("end should be after start: start=%v end=%v", start, end)
	}
	dur := end.Sub(start)
	if diff := time.Duration(24*time.Hour) - dur; diff < -5*time.Second || diff > 5*time.Second {
		t.Fatalf("duration not ~24h: got %v", dur)
	}
}

func TestCountFlagOccurrences(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
		want int
	}{
		{"none", []string{"cmd"}, "--extract", 0},
		{"space-form", []string{"cmd", "--extract", "a=b"}, "--extract", 1},
		{"equals-form", []string{"cmd", "--extract=a=b"}, "--extract", 1},
		{"multiple-mixed", []string{"cmd", "--extract", "x=y", "--other", "1", "--extract=a=b"}, "--extract", 2},
		{"value-looks-like-flag", []string{"cmd", "--extract", "-not-flag"}, "--extract", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := os.Args
			os.Args = tt.args
			defer func() { os.Args = old }()
			if got := CountFlagOccurrences(tt.flag); got != tt.want {
				t.Fatalf("CountFlagOccurrences(%q)=%d, want %d", tt.flag, got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		opts     *Options
		args     []string // for multi-extract case
		wantMsg  string
		wantCode int
	}{
		{"missing-filter", &Options{}, []string{"cmd"}, "", 2},
		{"next-without-extract", &Options{FilterPattern: "x", NextFilter: "nf"}, []string{"cmd"}, "error: --next-filter requires --extract", 2},
		{"ok", &Options{FilterPattern: "x"}, []string{"cmd"}, "", 0},
		{"multi-extract", &Options{FilterPattern: "x", Extract: "a=b"}, []string{"cmd", "--extract", "a=b", "--extract=c=d"}, "error: --extract specified multiple times", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := os.Args
			os.Args = tt.args
			defer func() { os.Args = old }()
			msg, code := tt.opts.Validate()
			if msg != tt.wantMsg || code != tt.wantCode {
				t.Fatalf("ValidateOptions()=(%q,%d), want (%q,%d)", msg, code, tt.wantMsg, tt.wantCode)
			}
		})
	}
}

func TestCollectOptions_Basic(t *testing.T) {
	withoutEnv("AWS_REGION", func() { // ensure region comes from flag
		withEnv("LOG_GROUP_NAMES", "g1,g2", func() {
			withFlagSet([]string{
				"aws-multi-log-inspector",
				"--filter-pattern", "ERROR",
				"--profile", "p1",
				"--region", "ap-northeast-1",
				"--extract", "id=foo",
				"--next-filter", "bar",
				"--pretty",
				// groups left as env default
			}, func() {
				o := CollectOptions()
				if o.FilterPattern != "ERROR" || o.Profile != "p1" || o.Region != "ap-northeast-1" || !o.PrettyJSON {
					t.Fatalf("CollectOptions returned unexpected values: %+v", o)
				}
				// groups from env
				if got := strings.TrimSpace(o.GroupsCSV); got != "g1,g2" {
					t.Fatalf("GroupsCSV=%q, want g1,g2", got)
				}
				if o.Extract != "id=foo" || o.NextFilter != "bar" {
					t.Fatalf("Extract/NextFilter mismatch: %+v", o)
				}
			})
		})
	})
}

func TestParseExtractSpec(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantN   string
		wantP   string
		wantErr bool
	}{
		{"simple", "id=foo.bar", "id", "foo.bar", false},
		{"trim-spaces", "  id =  foo.bar  ", "id", "foo.bar", false},
		{"multiple-equals", "n=a=b=c", "n", "a=b=c", false},
		{"missing-equals", "abc", "", "", true},
		{"empty-name", " =p", "", "", true},
		{"empty-path", "name=", "", "", true},
		{"path-trim-to-empty", "name=   ", "", "", true},
		{"path-trim-spaces", "n=  a.b  ", "n", "a.b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Options{Extract: tt.in}
			gotN, gotP, err := o.ParseExtractSpec()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got none (name=%q path=%q)", tt.in, gotN, gotP)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.in, err)
			}
			if gotN != tt.wantN || gotP != tt.wantP {
				t.Fatalf("ParseExtractSpec(%q)=(%q,%q), want (%q,%q)", tt.in, gotN, gotP, tt.wantN, tt.wantP)
			}
		})
	}
}
