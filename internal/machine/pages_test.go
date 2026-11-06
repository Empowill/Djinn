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
	"hw.pagesize":                     16384,
	"vm.page_free_count":              14338,
	"vm.page_speculative_count":       543,
	"vm.page_purgeable_count":         35292,
	"vm.page_pageable_external_count": 118839,
	"vm.page_pageable_internal_count": 385774,
}

// macVMStat is vm_stat on a 16 GB Mac whose Activity Monitor showed 14 GB used, its kernel 44 % available.
const macVMStat = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                5813.
Pages active:                            171050.
Pages inactive:                          168404.
Pages speculative:                         1500.
Pages wired down:                        233991.
Pages purgeable:                              2.
"Translation faults":                3736957646.
File-backed pages:                       119456.
Anonymous pages:                         221498.
Pages stored in compressor:             2009872.
Pages occupied by compressor:            429961.
`

// macUsed is Activity Monitor's Memory Used in macVMStat: app memory, wired and compressed.
const macUsed = (221498 - 2 + 233991 + 429961) * 16384

// TestDarwinAvailable takes what Activity Monitor leaves out of Memory Used, and the pages without vm_stat.
func TestDarwinAvailable(t *testing.T) {
	t.Parallel()
	const total = 16 << 30
	cases := []struct {
		name   string
		vmStat string
		counts map[string]uint32
		want   uint64
	}{
		{name: "vm_stat", vmStat: macVMStat, counts: macPages, want: total - macUsed},
		{name: "no vm_stat", counts: macPages, want: (14338 + 543 + 35292 + 118839) * 16384},
		{name: "vm_stat lacks a count", vmStat: "Pages wired down: 1.\n", counts: macPages,
			want: (14338 + 543 + 35292 + 118839) * 16384},
		{name: "nothing read", counts: map[string]uint32{}, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := darwinAvailable(total, []byte(c.vmStat), sysctls(c.counts)); got != c.want {
				t.Errorf("darwinAvailable = %d, want %d", got, c.want)
			}
		})
	}
}

// TestVMStatAvailable leaves out app memory, wired memory and what the compressor occupies, as Activity Monitor does.
func TestVMStatAvailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		total  uint64
		out    string
		want   uint64
		wantOK bool
	}{
		{name: "activity monitor", total: 16 << 30, out: macVMStat, want: 16<<30 - macUsed, wantOK: true},
		{name: "more used than total", total: 1 << 30, out: macVMStat, want: 0, wantOK: true},
		{name: "no page size", total: 16 << 30, out: "Anonymous pages: 1.\nPages purgeable: 0.\n" +
			"Pages wired down: 1.\nPages occupied by compressor: 1.\n"},
		{name: "total unknown", out: macVMStat},
		{name: "empty", total: 16 << 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok := vmStatAvailable(c.total, []byte(c.out))
			if got != c.want || ok != c.wantOK {
				t.Errorf("vmStatAvailable = %d, %v, want %d, %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestParseVMStat reads the page size from the header and each count by its label, quotes and final dot left out.
func TestParseVMStat(t *testing.T) {
	t.Parallel()
	page, counts := parseVMStat([]byte(macVMStat + "Pages garbled:   x.\n"))
	if page != 16384 {
		t.Errorf("page = %d, want 16384", page)
	}
	want := map[string]uint64{"Pages wired down": 233991, "Translation faults": 3736957646, "Pages purgeable": 2}
	for label, n := range want {
		if counts[label] != n {
			t.Errorf("counts[%q] = %d, want %d", label, counts[label], n)
		}
	}
	if _, ok := counts["Pages garbled"]; ok {
		t.Error("a count that is not a number was read")
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
