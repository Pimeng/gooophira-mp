package migration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MigrationPlan 包含旧配置迁移后生成的完整多文件结果。
// Files 中的路径相对于目标配置目录。
func (p *MigrationPlan) Names() []string {
	names := make([]string, 0, len(p.Files))
	for name := range p.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Write 创建计划中的所有文件，但不会覆盖现有配置。
// 首次写入前会预先检查全部冲突。
func (p *MigrationPlan) Write(dir string) error {
	for _, name := range p.Names() {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("refusing to overwrite existing file: %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range p.Names() {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, p.Files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}
