//go:build windows

package pluginartifact

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func physicalFileLinkCount(path string, _ fs.FileInfo) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	var information windows.ByHandleFileInformation
	statErr := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information)
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		return 0, errors.Join(statErr, closeErr)
	}
	return uint64(information.NumberOfLinks), nil
}
