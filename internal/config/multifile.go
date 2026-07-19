package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	CurrentFileVersion = 1
	CoreConfigFile     = "server.yaml"
)

var extensionConfigFiles = []string{
	"network.yaml",
	"replay.yaml",
	"redis.yaml",
	"webhook.yaml",
	"stats.yaml",
}

var configFileKeys = map[string][]string{
	CoreConfigFile: {
		"MONITORS", "TEST_ACCOUNT_IDS", "SERVER_NAME", "HOST", "PORT",
		"HTTP_SERVICE", "HTTP_PORT", "GUI", "ROOM_MAX_USERS",
		"ROOM_CREATION_ENABLED", "PLAYING_RECONNECT_GRACE", "MAX_ROOMS",
		"MAX_CONNECTIONS", "CONNECTION_RATE_LIMIT", "COMMAND_RATE_LIMIT",
		"HTTP_RATE_LIMIT_MAX_REQUESTS", "HTTP_RATE_LIMIT_WINDOW_MS",
		"CHAT_ENABLED", "SYSTEM_USER_ID", "ADMIN_TOKEN", "ADMIN_DATA_PATH",
		"ROOM_LIST_TIP", "LOG_LEVEL", "LOG_COMPRESS_AFTER_DAYS",
		"LOG_MAX_TOTAL_MB", "LANG", "PHIRA_API_ENDPOINT", "HITOKOTO_API_URL",
		"ALLOW_TOKEN_IN_QUERY",
		"AGENT_IPC",
	},
	"network.yaml": {
		"REAL_IP_HEADER", "CORS_ORIGINS", "HAPROXY_PROTOCOL", "OUTBOUND_PROXY", "NETUTIL",
	},
	"replay.yaml": {
		"REPLAY_ENABLED", "REPLAY_BASE_DIR", "REPLAY_TTL_DAYS", "REPLAY_AUTO_UPLOAD", "SHARE_STATION",
	},
	"redis.yaml": {
		"ENABLED", "HOST", "PORT", "PASSWORD", "DB",
	},
	"webhook.yaml": {
		"ENABLED", "TIMEOUT_MS", "RETRIES", "TARGETS",
	},
	"stats.yaml": {
		"STATS_DB_PATH", "STATS_DETAIL_RETENTION_DAYS", "STATS_DB_MAX_MB",
	},
}

// ConfigSet is one atomically loaded multi-file configuration snapshot.
// Config remains the runtime model used by the rest of the server. Files records
// which optional capabilities were explicitly installed by the administrator.
type ConfigSet struct {
	Config *ServerConfig
	Dir    string
	Files  map[string]bool
}

func (s *ConfigSet) HasFile(name string) bool {
	return s != nil && s.Files[name]
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

// LoadDir loads server.yaml and every known optional file that exists. Files are
// never discovered by glob, so editor backups cannot accidentally enable a feature.
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

	// Environment variables may override a module, but do not install it. This
	// preserves the central rule: an absent optional file means an absent feature.
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

func normalizeModuleMap(name string, raw map[string]any) (map[string]any, error) {
	switch name {
	case "network.yaml":
		if value, present := raw["NETUTIL"]; present {
			if err := validateRecordKeys("NETUTIL", value, []string{"DNS_SERVERS"}); err != nil {
				return nil, err
			}
		}
		return raw, nil
	case "replay.yaml":
		if value, present := raw["SHARE_STATION"]; present {
			if err := validateRecordKeys("SHARE_STATION", value, []string{"URL", "TOKEN"}); err != nil {
				return nil, err
			}
		}
		if _, present := raw["REPLAY_ENABLED"]; !present {
			raw["REPLAY_ENABLED"] = true
		}
		return raw, nil
	case "redis.yaml":
		if err := validateRedisMap(raw); err != nil {
			return nil, err
		}
		if _, present := raw["ENABLED"]; !present {
			raw["ENABLED"] = true
		}
		return map[string]any{"REDIS": raw}, nil
	case "webhook.yaml":
		if err := validateWebhookMap(raw); err != nil {
			return nil, err
		}
		if _, present := raw["ENABLED"]; !present {
			raw["ENABLED"] = true
		}
		return map[string]any{"WEBHOOK": raw}, nil
	case "stats.yaml":
		if _, present := raw["STATS_DB_PATH"]; !present {
			raw["STATS_DB_PATH"] = "stats.db"
		}
		return raw, nil
	case CoreConfigFile:
		if value, present := raw["AGENT_IPC"]; present {
			if err := validateRecordKeys("AGENT_IPC", value, []string{"ENDPOINT", "TOKEN", "DISCOVERY_FILE", "INSTANCE", "OUTBOX_DIR", "OUTBOX_MAX_MB", "WEBHOOK_OWNER"}); err != nil {
				return nil, err
			}
		}
		return raw, nil
	default:
		return raw, nil
	}
}

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

func validateRecordKeys(label string, value any, allowedKeys []string) error {
	record, ok := asRecord(value)
	if !ok {
		return fmt.Errorf("%s must be a mapping", label)
	}
	allowed := make(map[string]bool, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = true
	}
	for key := range record {
		if !allowed[key] {
			return fmt.Errorf("%s contains unknown key %s", label, key)
		}
	}
	return nil
}

func validateRedisMap(raw map[string]any) error {
	if value, present := raw["ENABLED"]; present {
		if _, ok := parseBoolValue(value); !ok {
			return fmt.Errorf("invalid value for ENABLED")
		}
	}
	if value, present := raw["HOST"]; present {
		if _, ok := parseStringValue(value); !ok {
			return fmt.Errorf("invalid value for HOST")
		}
	}
	if value, present := raw["PORT"]; present {
		if _, ok := parsePortValue(value); !ok {
			return fmt.Errorf("invalid value for PORT")
		}
	}
	if value, present := raw["PASSWORD"]; present {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("invalid value for PASSWORD")
		}
	}
	if value, present := raw["DB"]; present {
		if db, ok := asInt(value); !ok || db < 0 {
			return fmt.Errorf("invalid value for DB")
		}
	}
	return nil
}

var webhookTargetKeys = []string{
	"ID", "URL", "TYPE", "EVENTS", "SECRET", "ACCESS_TOKEN", "MESSAGE_TYPE", "TARGET_ID",
	"APP_ID", "APP_SECRET", "RECEIVE_OPEN_ID", "TEMPLATE_ID", "TEMPLATE_VERSION",
	"GAME_END_TEMPLATE_ID", "GAME_END_TEMPLATE_VERSION", "LIVE_UPDATE",
}

func validateWebhookMap(raw map[string]any) error {
	if value, present := raw["ENABLED"]; present {
		if _, ok := parseBoolValue(value); !ok {
			return fmt.Errorf("invalid value for ENABLED")
		}
	}
	if value, present := raw["TIMEOUT_MS"]; present {
		if n, ok := asInt(value); !ok || n <= 0 {
			return fmt.Errorf("invalid value for TIMEOUT_MS")
		}
	}
	if value, present := raw["RETRIES"]; present {
		if n, ok := asInt(value); !ok || n < 0 {
			return fmt.Errorf("invalid value for RETRIES")
		}
	}
	value, present := raw["TARGETS"]
	if !present {
		return nil
	}
	targets, ok := value.([]any)
	if !ok {
		return fmt.Errorf("TARGETS must be a list")
	}
	for i, target := range targets {
		if err := validateRecordKeys(fmt.Sprintf("TARGETS[%d]", i), target, webhookTargetKeys); err != nil {
			return err
		}
	}
	parsed, ok := parseWebhookValue(raw)
	if !ok || len(parsed.Targets) != len(targets) {
		return fmt.Errorf("TARGETS contains an invalid target")
	}
	return nil
}

func buildFromMapStrict(raw map[string]any, path string) (*ServerConfig, error) {
	known := make(map[string]fieldSpec, len(configFields))
	for _, field := range configFields {
		known[field.env] = field
	}
	for key, value := range raw {
		field, ok := known[key]
		if !ok {
			return nil, fmt.Errorf("%s: unsupported configuration key %s", path, key)
		}
		if _, valid := field.parse(value); !valid {
			return nil, fmt.Errorf("%s: invalid value for %s", path, key)
		}
	}
	return buildFromMap(raw, false), nil
}

func configFileForKey(key string) string {
	switch key {
	case "REAL_IP_HEADER", "CORS_ORIGINS", "HAPROXY_PROTOCOL", "OUTBOUND_PROXY", "NETUTIL":
		return "network.yaml"
	case "REPLAY_ENABLED", "REPLAY_BASE_DIR", "REPLAY_TTL_DAYS", "REPLAY_AUTO_UPLOAD", "SHARE_STATION":
		return "replay.yaml"
	case "REDIS":
		return "redis.yaml"
	case "WEBHOOK":
		return "webhook.yaml"
	case "STATS_DB_PATH", "STATS_DETAIL_RETENTION_DAYS", "STATS_DB_MAX_MB":
		return "stats.yaml"
	case "AGENT_IPC":
		return CoreConfigFile
	default:
		return CoreConfigFile
	}
}

const MinimalServerYAML = `# Phira MP 核心配置文件（首次启动自动生成）
# 未写出的项目使用内置默认值；完整中文说明见 config.example/server.yaml。
# 将 replay.yaml、redis.yaml、webhook.yaml、stats.yaml 或 network.yaml
# 放到本文件旁边，才会启用对应扩展；不要一次复制全部扩展示例。
# 配置优先级：命令行参数 > 环境变量 > 本文件 > 内置默认值。

# 配置格式版本。必须保留，当前只支持 1。
version: 1

# TCP 游戏服务监听地址；"::" 表示监听所有 IPv6 地址，通常也兼容 IPv4。
HOST: "::"
# TCP 游戏服务监听端口。
PORT: 12346
# 显示给玩家的服务器名称。
SERVER_NAME: "Phira MP"
# 服务端语言，影响日志、CLI 和 HTTP 默认输出。
LANG: "zh-CN"

# 可选 Agent 默认关闭。启用时可设为 auto，Agent 不在线不影响主程序。
# AGENT_IPC:
#   ENDPOINT: auto
`

// EnsureConfigDir creates only the required minimal server.yaml. Optional files
// are deliberately not copied because their presence enables capabilities.
func EnsureConfigDir(dir string) (bool, error) {
	path := filepath.Join(dir, CoreConfigFile)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(MinimalServerYAML), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
