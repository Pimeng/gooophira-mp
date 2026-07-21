package load

import (
	"os"
	"path/filepath"
)

type ConfigSet struct {
	Config *ServerConfig
	Dir    string
	Files  map[string]bool
}

func ConfigFileNames() []string {
	names := make([]string, 0, 1+len(extensionConfigFiles))
	names = append(names, CoreConfigFile)
	names = append(names, extensionConfigFiles...)
	return names
}

func ConfigDirExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, CoreConfigFile))
	return err == nil && !info.IsDir()
}

// LoadDir 加载 server.yaml 以及所有存在的已知可选文件。
// 文件不会通过 glob 发现，因此编辑器备份不会意外启用功能。
