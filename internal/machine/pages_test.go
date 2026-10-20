package machine

import (
	"errors"
	"testing"
)

// sysctls reads counts as sysctl would: an unknown name is an error.
func sysctls(counts map[string]uint32) func(string) (uint32, error) {
	return func(name string) (uint32, error) {
		n, ok := counts[name]
		if !ok {
			return 0, errors.New("unknown oid")
		}
		return n, nil
	}
}

// macPages are counts read on a 16 GB Mac whose Djinn showed its memory 95 % used, the file cache left out.
var macPages = map[string]uint32{
	"vm.page_free_count":              14338,
	"vm.page_speculative_count":       543,
	"vm.page_purgeable_count":         35292,
	"vm.page_pageable_external_count": 118839,
	"vm.page_pageable_internal_count": 385774,
}

// TestDarwinAvailable takes the kernel's own share of the memory available, which counts what the compressor can
// make room for, and the pages without it.
func TestDarwinAvailable(t *testing.T) {
	t.Parallel()
	const total = 16 << 30
	withLevel := func(level uint32) map[string]uint32 {
		counts := map[string]uint32{"kern.memorystatus_level": level, "hw.pagesize": 16384}
		for name, n := range macPages {
			counts[name] = n
		}
		return counts
	}
	pagesOnly := map[string]uint32{"hw.pagesize": 16384}
	for name, n := range macPages {
		pagesOnly[name] = n
	}
	cases := []struct {
		name   string
		counts map[string]uint32
		want   uint64
	}{
		{name: "kernel level", counts: withLevel(42), want: total / 100 * 42},
		{name: "no level", counts: pagesOnly, want: (14338 + 543 + 35292 + 118839) * 16384},
		{name: "level out of range", counts: withLevel(101), want: (14338 + 543 + 35292 + 118839) * 16384},
		{name: "nothing read", counts: map[string]uint32{}, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := darwinAvailable(total, sysctls(c.counts)); got != c.want {
				t.Errorf("darwinAvailable = %d, want %d", got, c.want)
			}
		})
	}
}

// TestPagesAvailable counts the file cache as available, as Linux's MemAvailable does: a Mac whose memory is mostly
// cache is not full.
func TestPagesAvailable(t *testing.T) {
	t.Parallel()
	const page = 16384
	cases := []struct {
		name   string
		total  uint64
		counts map[string]uint32
		want   uint64
	}{
		{name: "file cache counted", total: 16 << 30, counts: macPages, want: (14338 + 543 + 35292 + 118839) * page},
		{name: "count missing", total: 16 << 30, counts: map[string]uint32{"vm.page_free_count": 10}, want: 10 * page},
		{name: "at most total", total: 1 << 20, counts: macPages, want: 1 << 20},
		{name: "total unknown", counts: map[string]uint32{"vm.page_pageable_external_count": 2}, want: 2 * page},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := pagesAvailable(page, c.total, sysctls(c.counts)); got != c.want {
				t.Errorf("pagesAvailable = %d, want %d", got, c.want)
			}
		})
	}
}
