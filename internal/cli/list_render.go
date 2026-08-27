package cli

import (
	"io"
)

func renderList(writer io.Writer, result ListResult, flags Flags) error {
	return renderPowers(writer, result.Powers, flags.All, flags.JSON)
}
