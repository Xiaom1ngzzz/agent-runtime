package domain

// MemoryItem 是 Memory 层的存储单位。见 ch05 §5.3。
//
// 关键设计:
//   - Kind 分 semantic / episodic —— 允许 Query 时按类型过滤
//   - Version 支持幂等 Upsert
//   - ExpiresAt 支持 soft expire
//   - Origin* 保证跨 Session 溯源
type MemoryItem struct {
	// 身份
	ID     string
	Source string     // "user_pref" | "kb.docs" | "session_summary" | ...
	Kind   MemoryKind // semantic | episodic

	// 内容
	Key      string // 语义 key,如 "user:42:diet"
	Content  string // 文本;也是索引对象
	Metadata map[string]string

	// 索引
	Embedding []float32 // 可空;为空表示只支持 keyword/exact 查询
	Tags      []string

	// 生命周期
	CreatedAt string // Event ID 或 ISO 时间戳
	UpdatedAt string
	ExpiresAt string // 可空
	Version   int64  // upsert 时递增,支持幂等

	// 溯源(§5.3)
	OriginSession string
	OriginTaskID  string
	OriginSeqFrom int64
	OriginSeqTo   int64
}

// MemoryKind 见 ch05 §5.2.1。
type MemoryKind string

const (
	MemorySemantic MemoryKind = "semantic"
	MemoryEpisodic MemoryKind = "episodic"
)

// Query 是 MemoryStore.Query 的入参。见 ch05 §5.5。
type Query struct {
	// 至少填一个查询条件
	Semantic     string   // 走 embedding
	Keywords     []string // 走 keyword match
	Tags         []string // 精确匹配 tag
	KindFilter   MemoryKind
	SourceFilter []string

	// 结果控制
	TopK           int
	MinScore       float64
	IncludeExpired bool
}
