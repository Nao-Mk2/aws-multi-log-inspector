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

func TestResolveTimeWindow(t *testing.T) {
	fixedNow := time.Date(2025, 8, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		startStr  string
		endStr    string
		wantStart time.Time
		wantEnd   time.Time
		wantErr   bool
	}{
		{"both-empty", "", "", fixedNow.Add(-24 * time.Hour), fixedNow, false},
		{"only-start", "2025-08-30T10:00:00Z", "", time.Date(2025, 8, 30, 10, 0, 0, 0, time.UTC), time.Date(2025, 8, 31, 10, 0, 0, 0, time.UTC), false},
		{"only-end", "", "2025-08-31T10:00:00Z", time.Date(2025, 8, 30, 10, 0, 0, 0, time.UTC), time.Date(2025, 8, 31, 10, 0, 0, 0, time.UTC), false},
		{"both", "2025-08-30T09:00:00Z", "2025-08-31T09:30:00Z", time.Date(2025, 8, 30, 9, 0, 0, 0, time.UTC), time.Date(2025, 8, 31, 9, 30, 0, 0, time.UTC), false},
		{"start-after-end", "2025-08-31T12:01:00Z", "2025-08-31T12:00:00Z", time.Time{}, time.Time{}, true},
		{"bad-format", "not-time", "", time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd, err := ResolveTimeWindow(tt.startStr, tt.endStr, fixedNow)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none: start=%v end=%v", gotStart, gotEnd)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !gotStart.Equal(tt.wantStart) || !gotEnd.Equal(tt.wantEnd) {
				t.Fatalf("window mismatch: got [%v,%v], want [%v,%v]", gotStart, gotEnd, tt.wantStart, tt.wantEnd)
			}
		})
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
