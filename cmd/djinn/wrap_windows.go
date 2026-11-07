package main

import "errors"

// execCommand has no exec to run on Windows, where Djinn writes no wrapper.
func execCommand(string, []string, []string) error {
	return errors.New("the wrappers run on Linux and macOS only")
}
