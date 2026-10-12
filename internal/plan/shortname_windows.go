package plan

import "golang.org/x/sys/windows"

// systemShortName gives the 8.3 short name of path, as %TEMP% gives it (C:\Users\RUNNER~1): a prompt may hold a
// folder in this form while its project's folder is stored with long names.
func systemShortName(path string) (string, bool) {
	long, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return "", false
		}
		if int(n) < len(buf) { // Too small a buffer, it returns the size it needs, terminating zero included.
			return windows.UTF16ToString(buf[:n]), true
		}
		buf = make([]uint16, n)
	}
}
