package store

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestTimePtrYearBounds(t *testing.T) {
	cases := []struct {
		name    string
		v       sql.NullInt64
		wantNil bool
		wantYr  int
	}{
		{"null", sql.NullInt64{Valid: false}, true, 0},
		{"normal", sql.NullInt64{Int64: 1760634000, Valid: true}, false, 2025},
		{"year 9999", sql.NullInt64{Int64: MaxUnix, Valid: true}, false, 9999},
		{"year 10000 overflow", sql.NullInt64{Int64: MaxUnix + 1, Valid: true}, false, 9999},
		{"huge timestamp", sql.NullInt64{Int64: 1000000000000, Valid: true}, false, 9999},
		{"year 0", sql.NullInt64{Int64: MinUnix, Valid: true}, false, 0},
		{"negative year underflow", sql.NullInt64{Int64: MinUnix - 1, Valid: true}, false, 0},
		{"huge negative", sql.NullInt64{Int64: -1000000000000, Valid: true}, false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TimePtr(tc.v)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want non-nil")
			}
			if got.Year() != tc.wantYr {
				t.Fatalf("year = %d, want %d", got.Year(), tc.wantYr)
			}
			// Verify it marshals to JSON without error
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}
			if len(b) == 0 {
				t.Fatalf("empty json")
			}
		})
	}
}

func TestClampTime(t *testing.T) {
	normal := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if got := ClampTime(normal); got != normal {
		t.Fatalf("normal: got %v, want %v", got, normal)
	}
	overflow := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := ClampTime(overflow); got.Year() != 9999 {
		t.Fatalf("overflow: year = %d, want 9999", got.Year())
	}
	underflow := time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := ClampTime(underflow); got.Year() != 0 {
		t.Fatalf("underflow: year = %d, want 0", got.Year())
	}
}

func TestClampUnix(t *testing.T) {
	if got := ClampUnix(0); got != 0 {
		t.Fatalf("zero: got %d", got)
	}
	if got := ClampUnix(MaxUnix + 100); got != MaxUnix {
		t.Fatalf("overflow: got %d, want %d", got, MaxUnix)
	}
	if got := ClampUnix(MinUnix - 100); got != MinUnix {
		t.Fatalf("underflow: got %d, want %d", got, MinUnix)
	}
}
