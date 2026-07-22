package load

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
		"HTTP_SERVICE", "HTTP_PORT", "ROOM_MAX_USERS",
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

// ConfigSet 是一次原子加载得到的多文件配置快照。
// Config 仍是服务端其它部分使用的运行时模型，Files 记录管理员显式安装了哪些可选能力。

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
