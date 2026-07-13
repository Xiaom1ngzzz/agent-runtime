package memory

import (
	stdctx "context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-runtime-go/domain"
)

// InMemStore 是 ch05 §5.4.3 L1 档次的内存实现。用于测试与教学。
// 生产实现走 pgvector / Pinecone / sqlite-vss 等,见 §5.10。
type InMemStore struct {
	mu    sync.Mutex
	items map[string]domain.MemoryItem // key = MemoryItem.Key
	// nowFn 允许测试注入时钟。生产走 time.Now。§ADR-002 时钟通过参数传入的原则。
	nowFn func() time.Time
}

func NewInMemStore() *InMemStore {
	return &InMemStore{
		items: map[string]domain.MemoryItem{},
		nowFn: time.Now,
	}
}

// NewInMemStoreWithClock 允许注入时钟,测试用。
func NewInMemStoreWithClock(nowFn func() time.Time) *InMemStore {
	return &InMemStore{items: map[string]domain.MemoryItem{}, nowFn: nowFn}
}

// Upsert 幂等版本化写入。§5.8.1。
func (s *InMemStore) Upsert(_ stdctx.Context, item domain.MemoryItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := storeKey(item)
	if existing, ok := s.items[slot]; ok {
		if item.TenantID != "" && existing.TenantID != "" && item.TenantID != existing.TenantID {
			return fmt.Errorf("memory tenant mismatch for key %q: got %q, existing %q",
				item.Key, item.TenantID, existing.TenantID)
		}
		if item.Version < existing.Version {
			// 拒绝倒退,§5.9 MemoryUpsertRejected
			return fmt.Errorf("memory version regression for key %q: got %d, current %d",
				item.Key, item.Version, existing.Version)
		}
		if item.Version == existing.Version {
			// 幂等,忽略
			return nil
		}
	}
	if item.UpdatedAt == "" {
		item.UpdatedAt = s.nowFn().UTC().Format(time.RFC3339)
	}
	s.items[slot] = item
	return nil
}

// Query 按查询召回 Top-K。§5.5。
func (s *InMemStore) Query(_ stdctx.Context, q domain.Query) ([]domain.MemoryRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if q.TopK <= 0 {
		q.TopK = 10
	}
	if q.MinScore < 0 {
		q.MinScore = 0
	}
	now := s.nowFn()

	// 收集候选 + 打分。
	type scored struct {
		item  domain.MemoryItem
		score float64
	}
	var candidates []scored
	for _, item := range s.items {
		if q.TenantID != "" && item.TenantID != q.TenantID {
			continue
		}
		// 过期过滤
		if !q.IncludeExpired && item.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, item.ExpiresAt); err == nil && t.Before(now) {
				continue
			}
		}
		// Kind 过滤
		if q.KindFilter != "" && item.Kind != q.KindFilter {
			continue
		}
		// Source 过滤
		if len(q.SourceFilter) > 0 {
			ok := false
			for _, s := range q.SourceFilter {
				if item.Source == s {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		// Tags 过滤(item 必须含全部要求的 tag)
		if len(q.Tags) > 0 {
			if !hasAllTags(item.Tags, q.Tags) {
				continue
			}
		}
		// Keywords 匹配:任一 keyword 命中 Key 或 Content
		if len(q.Keywords) > 0 {
			hit := false
			for _, kw := range q.Keywords {
				if strings.Contains(item.Key, kw) || strings.Contains(item.Content, kw) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		// 打分:优先 semantic 相似(cosine),没有 embedding 时用 keyword-match score
		score := scoreItem(q, item)
		if score < q.MinScore {
			continue
		}
		candidates = append(candidates, scored{item: item, score: score})
	}

	// 按 score 降序,同 score 内按 Key 稳定排序(便于测试可复现)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].item.Key < candidates[j].item.Key
	})

	if len(candidates) > q.TopK {
		candidates = candidates[:q.TopK]
	}

	out := make([]domain.MemoryRef, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, domain.MemoryRef{
			Source:  c.item.Source,
			Key:     c.item.Key,
			Content: c.item.Content,
			Score:   c.score,
		})
	}
	return out, nil
}

// Expire 让指定 Key 立即过期。§5.8.2。
func (s *InMemStore) Expire(_ stdctx.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for slot, item := range s.items {
		if item.Key == key {
			item.ExpiresAt = s.nowFn().UTC().Add(-time.Second).Format(time.RFC3339)
			s.items[slot] = item
		}
	}
	return nil
}

// ---------- helpers ----------

func storeKey(item domain.MemoryItem) string {
	if item.TenantID != "" {
		return item.TenantID + "|" + item.Key
	}
	return item.Key
}

func hasAllTags(itemTags, required []string) bool {
	set := map[string]struct{}{}
	for _, t := range itemTags {
		set[t] = struct{}{}
	}
	for _, r := range required {
		if _, ok := set[r]; !ok {
			return false
		}
	}
	return true
}

// scoreItem 计算一条 item 相对 Query 的得分。
// 语义查询走 cosine,keyword 查询走 hit-count 归一化,tag-only 查询固定 1.0。
func scoreItem(q domain.Query, item domain.MemoryItem) float64 {
	if q.Semantic != "" && len(item.Embedding) > 0 {
		// L1 版没有真 embedder,教学期用一个"字符串重叠度"当假 semantic 分:
		// 生产实现替换为真实向量 cosine。这里保证与 embedding 长度无关。
		emb := fakeEmbed(q.Semantic, len(item.Embedding))
		return cosine(emb, item.Embedding)
	}
	if len(q.Keywords) > 0 {
		hits := 0
		for _, kw := range q.Keywords {
			if strings.Contains(item.Key, kw) || strings.Contains(item.Content, kw) {
				hits++
			}
		}
		return float64(hits) / float64(len(q.Keywords))
	}
	// 没有 semantic / keywords 时,tag/kind 已经在过滤阶段起作用,给一个固定分。
	return 1.0
}

// cosine 是标准的余弦相似度。为空向量或长度不匹配返回 0。
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// fakeEmbed 教学期的假 embedder:把字符串 hash 到 [-1, 1]^d。
// 保证同样输入同样输出(纯函数),这样测试可复现。生产替换为真 embedder。
func fakeEmbed(text string, dim int) []float32 {
	out := make([]float32, dim)
	if dim == 0 {
		return out
	}
	// 简单的滚动哈希:每个字符落到一个维度上。
	for i, ch := range text {
		out[i%dim] += float32(ch) / 1000.0
	}
	// 归一化到单位向量。
	var norm float64
	for _, v := range out {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		s := float32(1.0 / math.Sqrt(norm))
		for i := range out {
			out[i] *= s
		}
	}
	return out
}

// EmbedText 把文本转成用于索引的 embedding。教学期用 fakeEmbed;
// 生产环境注入真实 embedder(见 §5.10)。dim 是维度,和 Query 端一致即可。
func EmbedText(text string, dim int) []float32 {
	return fakeEmbed(text, dim)
}
