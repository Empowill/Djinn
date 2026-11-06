package machine

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// availablePages are the macOS page counts (sysctl) that a new program gets without compressing or swapping: free,
// speculative and purgeable pages, and the file-backed pages (the file cache), which macOS drops first. Leaving the
// file cache out, as before, read a Mac whose memory is mostly cache as full: Linux's MemAvailable counts it.
var availablePages = []string{
	"vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count", "vm.page_pageable_external_count",
}

// darwinAvailable gives the bytes available to new programs on macOS: what Activity Monitor leaves out of Memory Used
// (vmStatAvailable), from vmStat, the output of vm_stat; without it, the pages read gives (sysctl, pagesAvailable).
// The kernel's own share (kern.memorystatus_level) is not taken: it leaves out part of the compressed memory, and read
// a Mac at 44 % available while it reported warning pressure and Activity Monitor 14 of 16 GB used.
func darwinAvailable(total uint64, vmStat []byte, read func(name string) (uint32, error)) uint64 {
	if available, ok := vmStatAvailable(total, vmStat); ok {
		return available
	}
	page, err := read("hw.pagesize")
	if err != nil {
		return 0
	}
	return pagesAvailable(uint64(page), total, read)
}

// vmStatAvailable gives total less Activity Monitor's Memory Used, read from out, the output of vm_stat: app memory
// (anonymous pages less purgeable ones), wired memory and the pages the compressor occupies. It reports false when
// out lacks one of them or its page size.
func vmStatAvailable(total uint64, out []byte) (uint64, bool) {
	page, counts := parseVMStat(out)
	anonymous, ok1 := counts["Anonymous pages"]
	purgeable, ok2 := counts["Pages purgeable"]
	wired, ok3 := counts["Pages wired down"]
	compressed, ok4 := counts["Pages occupied by compressor"]
	if page == 0 || total == 0 || !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, false
	}
	used := (anonymous - min(purgeable, anonymous) + wired + compressed) * page
	if used >= total {
		return 0, true
	}
	return total - used, true
}

// parseVMStat reads vm_stat's output: its page size, from its first line ("… (page size of 16384 bytes)"), and its
// counts by label ("Pages wired down:   233991."); 0 and no count for what it cannot read.
func parseVMStat(out []byte) (uint64, map[string]uint64) {
	var page uint64
	counts := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if _, after, ok := strings.Cut(line, "page size of "); ok {
			if n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(after), " bytes)"), 10, 64); err == nil {
				page = n
			}
			continue
		}
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64); err == nil {
			counts[strings.Trim(label, `"`)] = n
		}
	}
	return page, counts
}

// pagesAvailable gives the bytes available to new programs from the page counts read gives, at most total when it is
// known; a count read cannot give is left out.
func pagesAvailable(page, total uint64, read func(name string) (uint32, error)) uint64 {
	var pages uint64
	for _, name := range availablePages {
		if n, err := read(name); err == nil {
			pages += uint64(n)
		}
	}
	available := pages * page
	if total > 0 && available > total {
		return total
	}
	return available
}
