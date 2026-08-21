package main

import (
	"fmt"
	"path/filepath"
)

type localRootComponentInspection struct {
	directory    bool
	reparsePoint bool
}

// validatePhysicalLocalRootWithInspector 从文件系统根向下检查每个组件，并拒绝任何重解析节点或非目录。
func validatePhysicalLocalRootWithInspector(root string, inspect func(string) (localRootComponentInspection, error)) error {
	components := make([]string, 0, 8)
	for current := root; ; current = filepath.Dir(current) {
		components = append(components, current)
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	for index := len(components) - 1; index >= 0; index-- {
		component := components[index]
		inspection, err := inspect(component)
		if err != nil {
			return fmt.Errorf("inspect Local resource root component %q: %w", component, err)
		}
		if inspection.reparsePoint {
			return fmt.Errorf("Local resource root component %q is a reparse point", component)
		}
		if !inspection.directory {
			return fmt.Errorf("Local resource root component %q is not a directory", component)
		}
	}
	return nil
}
