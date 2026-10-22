package api

import (
	"errors"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// An order names every row once; anything else is refused before a row is numbered.
func TestSetOrder(t *testing.T) {
	have := []int64{4, 7, 9}
	for _, c := range []struct {
		name string
		ids  []int64
		ok   bool
	}{
		{"every row once", []int64{9, 4, 7}, true},
		{"a row twice", []int64{4, 4, 7}, false},
		{"a row that is not there", []int64{4, 7, 8}, false},
		{"a row missing", []int64{4, 7}, false},
		{"a row too many", []int64{4, 7, 9, 4}, false},
		{"none", nil, false},
	} {
		placed := map[int64]int64{}
		err := setOrder(c.ids, have, "order_mismatch", func(id, place int64) error {
			placed[id] = place
			return nil
		})
		if c.ok {
			if err != nil || len(placed) != 3 || placed[9] != 1 || placed[4] != 2 || placed[7] != 3 {
				t.Errorf("%s: %v %v", c.name, err, placed)
			}
			continue
		}
		var he huma.StatusError
		if !errors.As(err, &he) || he.GetStatus() != 422 || len(placed) != 0 {
			t.Errorf("%s: want a 422 and no row numbered, got %v %v", c.name, err, placed)
		}
	}
	// A failing write stops the numbering and comes back as it is.
	boom := errors.New("boom")
	if err := setOrder([]int64{4, 7, 9}, have, "order_mismatch", func(int64, int64) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("write error: %v", err)
	}
}
