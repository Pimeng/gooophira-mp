//go:build !windows

package agentipc

import (
	"os"

	"github.com/Pimeng/gooophira-mp/internal/common/platform/securepath"
)

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

func restrictFileToCurrentUser(path string) error {
	return securepath.RestrictToCurrentUser(path)
}
