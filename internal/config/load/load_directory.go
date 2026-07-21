package load

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	CurrentFileVersion = 1
	CoreConfigFile     = "server.yaml"
)

func (s *ConfigSet) HasFile(name string) bool {
	return s != nil && s.Files[name]
}

func LoadDir(dir string) (*ConfigSet, error) {
	corePath := filepath.Join(dir, CoreConfigFile)
	coreRaw, err := loadVersionedMap(corePath, configFileKeys[CoreConfigFile], true)
	if err != nil {
		return nil, err
	}

	combined, err := buildFromMapStrict(coreRaw, corePath)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{CoreConfigFile: true}
	for _, name := range extensionConfigFiles {
		path := filepath.Join(dir, name)
		raw, readErr := loadVersionedMap(path, configFileKeys[name], false)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		files[name] = true

		moduleRaw, mapErr := normalizeModuleMap(name, raw)
		if mapErr != nil {
			return nil, fmt.Errorf("%s: %w", path, mapErr)
		}
		moduleCfg, buildErr := buildFromMapStrict(moduleRaw, path)
		if buildErr != nil {
			return nil, buildErr
		}
		combined = Merge(combined, moduleCfg)
	}

	// 环境变量可以覆盖模块，但不能安装模块。这样可保持核心规则：
	// 可选文件不存在，就表示对应功能不存在。
	env, err := loadDirEnv(files)
	if err != nil {
		return nil, err
	}
	combined = Merge(combined, env)

	if combined.EffectiveReplayAutoUpload() && !combined.ShareStationConfigured() {
		return nil, fmt.Errorf("%s: REPLAY_AUTO_UPLOAD requires SHARE_STATION", filepath.Join(dir, "replay.yaml"))
	}

	return &ConfigSet{Config: combined, Dir: dir, Files: files}, nil
}
