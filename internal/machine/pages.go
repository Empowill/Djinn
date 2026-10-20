package machine

// availablePages are the macOS page counts (sysctl) that a new program gets without compressing or swapping: free,
// speculative and purgeable pages, and the file-backed pages (the file cache), which macOS drops first. Leaving the
// file cache out, as before, read a Mac whose memory is mostly cache as full: Linux's MemAvailable counts it.
var availablePages = []string{
	"vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count", "vm.page_pageable_external_count",
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
