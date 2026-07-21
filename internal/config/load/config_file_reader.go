package load

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func loadVersionedMap(path string, keys []string, required bool) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("%s: configuration file is empty", path)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: configuration must be a YAML mapping", path)
	}

	versionRaw, present := raw["version"]
	if !present {
		versionRaw, present = raw["VERSION"]
	}
	version, ok := asInt(versionRaw)
	if !present || !ok {
		return nil, fmt.Errorf("%s: version must be an integer", path)
	}
	if version != CurrentFileVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (current %d)", path, version, CurrentFileVersion)
	}
	delete(raw, "version")
	delete(raw, "VERSION")

	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	var unknown []string
	for key := range raw {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s: unknown configuration keys: %s", path, strings.Join(unknown, ", "))
	}
	return raw, nil
}
