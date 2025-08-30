package util_test

import (
	"testing"

	util "aws-multi-log-inspector/internal/util"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func strptr(s string) *string { return &s }

func TestExtractFirstValue(t *testing.T) {
	tests := []struct {
		name     string
		messages []*string
		jmes     string
		want     string
		wantOK   bool
		wantErr  bool
	}{
		{
			name:     "JSON field extraction",
			messages: []*string{strptr(`{"user":{"id":"123"}}`)},
			jmes:     "user.id",
			want:     "123",
			wantOK:   true,
		},
		{
			name:     "Non-JSON wraps as message",
			messages: []*string{strptr("WARN: something")},
			jmes:     "message",
			want:     "WARN: something",
			wantOK:   true,
		},
		{
			name:     "Array result takes first element",
			messages: []*string{strptr(`{"ids":["a","b"]}`)},
			jmes:     "ids",
			want:     "a",
			wantOK:   true,
		},
		{
			name:     "Empty result returns not found",
			messages: []*string{strptr(`{"user":{}}`)},
			jmes:     "user.id",
			want:     "",
			wantOK:   false,
		},
		{
			name:     "Invalid JMESPath returns error",
			messages: []*string{strptr(`{"a":1}`)},
			jmes:     "user.[",
			wantErr:  true,
		},
		{
			name:     "Non-string value marshaled to JSON",
			messages: []*string{strptr(`{"n":42}`)},
			jmes:     "n",
			want:     "42",
			wantOK:   true,
		},
		{
			name:     "Slice first empty then next event non-empty",
			messages: []*string{strptr(`{"names":[""]}`), strptr(`{"names":["ok"]}`)},
			jmes:     "names",
			want:     "ok",
			wantOK:   true,
		},
		{
			name:     "Nil message skipped then use next",
			messages: []*string{nil, strptr(`{"v":"x"}`)},
			jmes:     "v",
			want:     "x",
			wantOK:   true,
		},
		{
			name:     "Empty raw message yields not found",
			messages: []*string{strptr("")},
			jmes:     "message",
			want:     "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs := make([]types.FilteredLogEvent, 0, len(tt.messages))
			for _, m := range tt.messages {
				evs = append(evs, types.FilteredLogEvent{Message: m})
			}
			got, ok, err := util.ExtractFirstValue(evs, tt.jmes)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok mismatch: got %v want %v (value=%q)", ok, tt.wantOK, got)
			}
			if got != tt.want {
				t.Fatalf("value mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNextFilter(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		extracted string
		want      string
		// When fallback is expected, want is expr
	}{
		{
			name:      "Simple value passthrough",
			expr:      "value",
			extracted: "abc",
			want:      "abc",
		},
		{
			name:      "Non-string result marshaled to JSON",
			expr:      "[value, 'x']",
			extracted: "abc",
			want:      "[\"abc\",\"x\"]",
		},
		{
			name:      "join builds expected string",
			expr:      "join('', ['@message = \"', value, '\"'])",
			extracted: "abc",
			want:      "@message = \"abc\"",
		},
		{
			name:      "Invalid JMES falls back to literal",
			expr:      "user.[",
			extracted: "abc",
			want:      "user.[",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.BuildNextFilter(tt.expr, tt.extracted)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("result mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestReplacePlaceholder(t *testing.T) {
	tests := []struct {
		name string
		expr string
		key  string
		val  string
		want string
	}{
		{
			name: "Replaces with JSON-quoted value",
			expr: "@message = {{value}}",
			key:  "value",
			val:  "a\"b",
			want: "@message = \"a\\\"b\"",
		},
		{
			name: "No name returns original",
			expr: "prefix {{x}} suffix",
			key:  "",
			val:  "ignored",
			want: "prefix {{x}} suffix",
		},
		{
			name: "Name not present leaves unchanged",
			expr: "{{foo}} and more",
			key:  "bar",
			val:  "v",
			want: "{{foo}} and more",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := util.ReplacePlaceholder(tt.expr, tt.key, tt.val)
			if got != tt.want {
				t.Fatalf("result mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}
