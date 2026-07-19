package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

const (
	defaultConfigPath = "server_config.yml"
	defaultConfigDir  = "config"
)

type startupConfig struct {
	cfg        *config.ServerConfig
	created    bool
	legacy     bool
	path       string
	dir        string
	configSet  *config.ConfigSet
	fromLegacy bool
}

func flagWasSet(names ...string) bool {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	found := false
	flag.Visit(func(f *flag.Flag) {
		if wanted[f.Name] {
			found = true
		}
	})
	return found
}

func loadStartupConfig(configPath, configDir string) (*startupConfig, error) {
	legacyExplicit := flagWasSet("config", "c")
	dirExplicit := flagWasSet("config-dir")
	if legacyExplicit && dirExplicit {
		return nil, fmt.Errorf("-config and -config-dir cannot be used together")
	}

	// 现有部署在完成迁移前继续使用旧配置文件；新部署以及显式指定
	// -config-dir 的启动方式使用目录布局。
	useLegacy := legacyExplicit
	if !legacyExplicit && !dirExplicit && !config.ConfigDirExists(configDir) {
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			useLegacy = true
		}
	}
	if useLegacy {
		created, err := config.EnsureDefaultFile(configPath)
		if err != nil {
			return nil, err
		}
		cfg, fromFile, err := config.LoadMerged(configPath)
		if err != nil {
			return nil, err
		}
		return &startupConfig{
			cfg: cfg, created: created, legacy: true, path: configPath,
			fromLegacy: fromFile,
		}, nil
	}

	created, err := config.EnsureConfigDir(configDir)
	if err != nil {
		return nil, err
	}
	set, err := config.LoadDir(configDir)
	if err != nil {
		return nil, err
	}
	return &startupConfig{
		cfg: set.Config, created: created, path: filepath.Join(configDir, config.CoreConfigFile),
		dir: configDir, configSet: set,
	}, nil
}

func (s *startupConfig) reload() (*config.ServerConfig, error) {
	if s.legacy {
		cfg, _, err := config.LoadMerged(s.path)
		return cfg, err
	}
	set, err := config.LoadDir(s.dir)
	if err != nil {
		return nil, err
	}
	s.configSet = set
	return set.Config, nil
}

func (s *startupConfig) watcher(onChange func()) *config.FileWatcher {
	if s.legacy {
		return config.NewFileWatcher(s.path, 0, onChange)
	}
	return config.NewConfigDirWatcher(s.dir, 0, onChange)
}

func (s *startupConfig) extensionEnabled(name string) bool {
	return s.legacy || s.configSet.HasFile(name)
}
