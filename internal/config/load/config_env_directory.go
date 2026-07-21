package load

import (
	"fmt"
	"os"
)

func loadDirEnv(files map[string]bool) (*ServerConfig, error) {
	c := &ServerConfig{}
	for _, field := range configFields {
		name := configFileForKey(field.env)
		if name != CoreConfigFile && !files[name] {
			continue
		}
		var raw any
		var present bool
		if field.envInput != nil {
			raw, present = field.envInput()
		} else {
			raw, present = os.LookupEnv(field.env)
			if present && raw == "" {
				present = false
			}
		}
		if !present {
			continue
		}
		value, ok := field.parse(raw)
		if !ok {
			return nil, fmt.Errorf("environment variable for %s has an invalid value", field.env)
		}
		field.set(c, value)
	}
	return c, nil
}
