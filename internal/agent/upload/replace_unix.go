//go:build !windows

package agentupload

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
