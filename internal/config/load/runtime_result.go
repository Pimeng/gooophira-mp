package load

type RuntimePatchResult struct {
	OK   bool
	Keys []string       // 成功解析的 ENV 名（排序）
	rt   []rtPatchEntry // 内部：应用条目
	// Persist 是落盘补丁（ENV 名 → 标量），用于 PersistConfigValues。
	Persist map[string]any

	InvalidKeys     []string // 值非法
	StartupOnlyKeys []string // 仅启动期生效，运行时不可改
	UnsupportedKeys []string // 未知或不支持运行时改动
	Empty           bool     // 无任何可应用键
}

type rtPatchEntry struct {
	desc  *runtimeDescriptor
	value any
}

// Apply 把已解析的补丁应用到 cfg（含 normalize）。仅在 OK 时有意义。
