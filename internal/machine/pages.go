package machine

// availablePages are the macOS page counts (sysctl) that a new program gets without compressing or swapping: free,
// speculative and purgeable pages, and the file-backed pages (the file cache), which macOS drops first. Leaving the
// file cache out, as before, read a Mac whose memory is mostly cache as full: Linux's MemAvailable counts it.
var availablePages = []string{
	"vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count", "vm.page_pageable_external_count",
}

// darwinAvailable gives the bytes available to new programs on macOS from what read gives (sysctl): the share of the
// memory the kernel reports available (kern.memorystatus_level, in percent), the one its memory pressure follows. It
// counts what the compressor can make room for, always on in macOS where Linux's swap is not: from the pages alone,
// a 16 GB Mac at 42 % read 2.4 GB, and its 2 GiB reserve left room for one worker. Without the level, the pages
// (pagesAvailable).
func darwinAvailable(total uint64, read func(name string) (uint32, error)) uint64 {
	if level, err := read("kern.memorystatus_level"); err == nil && level > 0 && level <= 100 {
		return total / 100 * uint64(level)
	}
	page, err := read("hw.pagesize")
	if err != nil {
		return 0
	}
	return pagesAvailable(uint64(page), total, read)
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
