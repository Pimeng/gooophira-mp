package model

type Chart struct {
	ID   int
	Name string

	// Level 是谱面难度等级展示字符串（如 "IN Lv.15"）。对应 API 字段 level。
	Level string
	// Charter 是谱师署名。对应 API 字段 charter。
	Charter string
	// Illustration 是谱面封面图 URL（指向 phira.5wyxi.com/files/...）。
	// 飞书模板变量 chart_pic 由该 URL 下载后上传飞书换 image_key 填入。
	Illustration string
}

// RecordData 是 Phira API /record/:id 返回的成绩数据。
//
// Chart 用指针：旧客户端/API 未返回谱面 ID 时为 nil，此时跳过「成绩谱面是否与房间
// 当前谱面一致」的校验（fail-open，避免误伤正常玩家）。

type RecordData struct {
	ID        int
	Player    int
	Chart     *int // 该成绩对应的谱面 ID；nil = API 未返回，跳过校验
	Score     int
	Perfect   int
	Good      int
	Bad       int
	Miss      int
	MaxCombo  int
	Accuracy  float64
	Mod       int
	FullCombo bool
	Std       *float64 // nil => JSON 里的 null
	StdScore  *float64 // nil => JSON 里的 null
}
