package inspector_test

import (
	"aws-multi-log-inspector/internal/inspector"
	"aws-multi-log-inspector/internal/model"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type searchCall struct {
	group   string
	filter  string
	startMs int64
	endMs   int64
}

type mockRetriever struct {
	mu      sync.Mutex
	results map[string][]model.LogRecord
	errFor  map[string]error
	calls   []searchCall
}

func (m *mockRetriever) SearchGroup(ctx context.Context, group, filterPattern string, startMs, endMs int64) ([]model.LogRecord, error) {
	m.mu.Lock()
	m.calls = append(m.calls, searchCall{group: group, filter: filterPattern, startMs: startMs, endMs: endMs})
	var (
		err error
		out []model.LogRecord
		ok  bool
	)
	if m.errFor != nil {
		err = m.errFor[group]
	}
	if m.results != nil {
		out, ok = m.results[group]
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if ok {
		return out, nil
	}
	return nil, nil
}

func TestInspectorSearch(t *testing.T) {
	start := time.UnixMilli(500)
	end := time.UnixMilli(5000)
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	g1 := "/g1"
	g2 := "/g2"

	r1 := model.LogRecord{Timestamp: time.UnixMilli(1000), LogGroup: g1, LogStream: "s2", Message: "b"}
	r2 := model.LogRecord{Timestamp: time.UnixMilli(2000), LogGroup: g2, LogStream: "s1", Message: "m"}
	r3 := model.LogRecord{Timestamp: time.UnixMilli(3000), LogGroup: g1, LogStream: "s1", Message: "z"}
	r4 := model.LogRecord{Timestamp: time.UnixMilli(3000), LogGroup: g1, LogStream: "s1", Message: "a"}

	tests := []struct {
		name        string
		groups      []string
		filter      string
		setupMock   func() *mockRetriever
		wantRecords []model.LogRecord
		wantErr     bool
		wantFilter  string // expected filter passed to retriever (checked for all calls)
	}{
		{
			name:   "no groups configured returns error",
			groups: nil,
			filter: "anything",
			setupMock: func() *mockRetriever {
				return &mockRetriever{}
			},
			wantErr: true,
		},
		{
			name:      "empty filter returns error",
			groups:    []string{g1},
			filter:    "",
			setupMock: func() *mockRetriever { return &mockRetriever{} },
			wantErr:   true,
		},
		{
			name:   "quotes filter and aggregates sorted",
			groups: []string{g1, g2},
			filter: "hello",
			setupMock: func() *mockRetriever {
				return &mockRetriever{
					results: map[string][]model.LogRecord{
						g1: {r1, r3, r4},
						g2: {r2},
					},
				}
			},
			// Expected overall order by timestamp, then group, stream, message
			wantRecords: []model.LogRecord{r1, r2, r4, r3},
			wantFilter:  "\"hello\"",
		},
		{
			name:        "already quoted filter unchanged",
			groups:      []string{g1},
			filter:      "\"WARN\"",
			setupMock:   func() *mockRetriever { return &mockRetriever{results: map[string][]model.LogRecord{g1: {}}} },
			wantRecords: nil,
			wantFilter:  "\"WARN\"",
		},
		{
			name:   "propagates retriever error",
			groups: []string{g1},
			filter: "err",
			setupMock: func() *mockRetriever {
				return &mockRetriever{errFor: map[string]error{g1: errors.New("boom")}}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := tt.setupMock()
			in := inspector.New(mr, tt.groups, start, end)

			got, err := in.Search(context.Background(), tt.filter)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			// Verify calls (count and parameters)
			if len(tt.groups) != len(mr.calls) {
				t.Fatalf("calls = %d, want %d", len(mr.calls), len(tt.groups))
			}
			for _, c := range mr.calls {
				if c.startMs != startMs || c.endMs != endMs {
					t.Fatalf("start/end passed = (%d,%d), want (%d,%d)", c.startMs, c.endMs, startMs, endMs)
				}
				if tt.wantFilter != "" && c.filter != tt.wantFilter {
					t.Fatalf("filter passed = %q, want %q", c.filter, tt.wantFilter)
				}
			}

			// Verify records order/content
			if len(got) != len(tt.wantRecords) {
				t.Fatalf("records len = %d, want %d", len(got), len(tt.wantRecords))
			}
			for i := range tt.wantRecords {
				gr, wr := got[i], tt.wantRecords[i]
				if !gr.Timestamp.Equal(wr.Timestamp) || gr.LogGroup != wr.LogGroup || gr.LogStream != wr.LogStream || gr.Message != wr.Message {
					t.Fatalf("record[%d] = %+v, want %+v", i, gr, wr)
				}
			}
		})
	}
}
