//go:build !windows

package main

import "os"

func validatePhysicalLocalRoot(root string) error {
	return validatePhysicalLocalRootWithInspector(root, func(component string) (localRootComponentInspection, error) {
		info, err := os.Lstat(component)
		if err != nil {
			return localRootComponentInspection{}, err
		}
		return localRootComponentInspection{
			directory:    info.IsDir(),
			reparsePoint: info.Mode()&os.ModeSymlink != 0,
		}, nil
	})
}
