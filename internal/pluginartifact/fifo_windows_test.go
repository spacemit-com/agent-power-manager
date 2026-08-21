//go:build windows

package pluginartifact_test

import "testing"

func createFIFO(t *testing.T, _ string) {
	t.Helper()
	t.Skip("Windows does not provide FIFO filesystem nodes")
}
