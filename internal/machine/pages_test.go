package machine

import (
	"errors"
	"testing"
)

// TestPagesAvailable counts the file cache as available, as Linux's MemAvailable does: a Mac whose memory is mostly
// cache is not full.
func TestPagesAvailable(t *testing.T) {
	t.Parallel()
	const page = 16384
	// Counts read on a 16 GB Mac whose Djinn showed its memory 95 % used, the file cache left out.
	mac := map[string]uint32{
		"vm.page_free_count":              14338,
		"vm.page_speculative_count":       543,
		"vm.page_purgeable_count":         35292,
		"vm.page_pageable_external_count": 118839,
		"vm.page_pageable_internal_count": 385774,
	}
	cases := []struct {
		name   string
		total  uint64
		counts map[string]uint32
		want   uint64
	}{
		{name: "file cache counted", total: 16 << 30, counts: mac, want: (14338 + 543 + 35292 + 118839) * page},
		{name: "count missing", total: 16 << 30, counts: map[string]uint32{"vm.page_free_count": 10}, want: 10 * page},
		{name: "at most total", total: 1 << 20, counts: mac, want: 1 << 20},
		{name: "total unknown", counts: map[string]uint32{"vm.page_pageable_external_count": 2}, want: 2 * page},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			read := func(name string) (uint32, error) {
				n, ok := c.counts[name]
				if !ok {
					return 0, errors.New("unknown oid")
				}
				return n, nil
			}
			if got := pagesAvailable(page, c.total, read); got != c.want {
				t.Errorf("pagesAvailable = %d, want %d", got, c.want)
			}
		})
	}
}
