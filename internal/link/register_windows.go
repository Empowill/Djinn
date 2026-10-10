package link

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// Register makes exe the handler of djinn:// links for the user, in HKEY_CURRENT_USER: the install does it, and
// replaces another handler.
func Register(exe string) error {
	for _, v := range RegistryValues(exe) {
		path := RegistryKey
		if v.Key != "" {
			path += `\` + v.Key
		}
		k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
		if err != nil {
			return err
		}
		err = k.SetStringValue(v.Name, v.Data)
		k.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Registered tells whether the user has a handler of djinn:// links, whichever djinn it runs.
func Registered() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, RegistryKey+`\shell\open\command`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	command, _, err := k.GetStringValue("")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return command != "", err
}
