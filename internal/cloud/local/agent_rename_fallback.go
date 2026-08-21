package local

import (
	"errors"
	"fmt"
)

type linkFile func(string, string) error
type removeFile func(string) error

func linkAgentNoReplace(source, destination string, link linkFile, remove removeFile) error {
	if err := link(source, destination); err != nil {
		return err
	}
	if err := remove(source); err != nil {
		rollbackErr := remove(destination)
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("remove Agent temporary source %q: %w", source, err),
				fmt.Errorf("roll back Agent destination %q: %w", destination, rollbackErr),
			)
		}
		return fmt.Errorf("remove Agent temporary source %q: %w", source, err)
	}
	return nil
}
