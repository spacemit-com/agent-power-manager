//go:build windows

package main

import "golang.org/x/sys/windows"

func validatePhysicalLocalRoot(root string) error {
	return validatePhysicalLocalRootWithInspector(root, inspectWindowsLocalRootComponent)
}

func inspectWindowsLocalRootComponent(component string) (localRootComponentInspection, error) {
	path, err := windows.UTF16PtrFromString(component)
	if err != nil {
		return localRootComponentInspection{}, err
	}
	handle, err := windows.CreateFile(
		path,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return localRootComponentInspection{}, err
	}
	var information windows.ByHandleFileInformation
	informationErr := windows.GetFileInformationByHandle(handle, &information)
	closeErr := windows.CloseHandle(handle)
	if informationErr != nil {
		return localRootComponentInspection{}, informationErr
	}
	if closeErr != nil {
		return localRootComponentInspection{}, closeErr
	}
	return localRootComponentInspection{
		directory:    information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0,
		reparsePoint: information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0,
	}, nil
}
