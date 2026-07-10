//! MemoryStore —— 与 `runtime-go/memory/` 对齐。
//!
//! 见 ch05。Memory 与 EventStore 是正交的两个轴:EventStore 不可变、单 Session、精确;
//! Memory 可变、跨 Session、模糊。Retrieval 不在 Assemble 里发生(§5.2.2)。

use std::collections::HashMap;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};

use crate::summary::MemoryRef;

/// MemoryItem 是 Memory 层的存储单位。§5.3。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MemoryItem {
    pub id: String,
    pub source: String,
    pub kind: MemoryKind,

    pub key: String,
    pub content: String,
    #[serde(default)]
    pub metadata: HashMap<String, String>,

    #[serde(default)]
    pub embedding: Vec<f32>,
    #[serde(default)]
    pub tags: Vec<String>,

    #[serde(default)]
    pub created_at: String,
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub expires_at: String,
    pub version: i64,

    #[serde(default)]
    pub origin_session: String,
    #[serde(default)]
    pub origin_task_id: String,
    #[serde(default)]
    pub origin_seq_from: i64,
    #[serde(default)]
    pub origin_seq_to: i64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum MemoryKind {
    #[default]
    Semantic,
    Episodic,
}

/// Query 见 §5.5。
#[derive(Debug, Clone, Default)]
pub struct Query {
    pub semantic: String,
    pub keywords: Vec<String>,
    pub tags: Vec<String>,
    pub kind_filter: Option<MemoryKind>,
    pub source_filter: Vec<String>,
    pub top_k: usize,
    pub min_score: f64,
    pub include_expired: bool,
}

#[derive(Debug)]
pub struct MemoryError(pub String);

/// MemoryStore 接口。§5.4。
pub trait MemoryStore {
    fn upsert(&self, item: MemoryItem) -> Result<(), MemoryError>;
    fn query(&self, q: &Query) -> Result<Vec<MemoryRef>, MemoryError>;
    fn expire(&self, key: &str) -> Result<(), MemoryError>;
}

// ---------- L1 内存实现 ----------

pub struct InMemStore {
    items: Mutex<HashMap<String, MemoryItem>>,
}

impl Default for InMemStore {
    fn default() -> Self {
        Self::new()
    }
}

impl InMemStore {
    pub fn new() -> Self {
        Self { items: Mutex::new(HashMap::new()) }
    }
}

impl MemoryStore for InMemStore {
    fn upsert(&self, item: MemoryItem) -> Result<(), MemoryError> {
        let mut guard = self.items.lock().unwrap();
        if let Some(existing) = guard.get(&item.key) {
            if item.version < existing.version {
                return Err(MemoryError(format!(
                    "memory version regression for key {:?}: got {}, current {}",
                    item.key, item.version, existing.version
                )));
            }
            if item.version == existing.version {
                return Ok(()); // 幂等
            }
        }
        guard.insert(item.key.clone(), item);
        Ok(())
    }

    fn query(&self, q: &Query) -> Result<Vec<MemoryRef>, MemoryError> {
        let top_k = if q.top_k == 0 { 10 } else { q.top_k };
        let min_score = q.min_score.max(0.0);

        let guard = self.items.lock().unwrap();

        let mut scored: Vec<(MemoryItem, f64)> = Vec::new();
        for item in guard.values() {
            // Kind 过滤
            if let Some(k) = q.kind_filter {
                if item.kind != k {
                    continue;
                }
            }
            // Source 过滤
            if !q.source_filter.is_empty() && !q.source_filter.iter().any(|s| s == &item.source) {
                continue;
            }
            // Tags 过滤
            if !q.tags.is_empty() && !has_all_tags(&item.tags, &q.tags) {
                continue;
            }
            // Keywords 过滤
            if !q.keywords.is_empty() {
                let hit = q.keywords.iter().any(|kw|
                    item.key.contains(kw) || item.content.contains(kw)
                );
                if !hit {
                    continue;
                }
            }
            let s = score_item(q, item);
            if s < min_score {
                continue;
            }
            scored.push((item.clone(), s));
        }

        // 排序:score 降序,同分按 key 稳定
        scored.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| a.0.key.cmp(&b.0.key))
        });
        if scored.len() > top_k {
            scored.truncate(top_k);
        }

        Ok(scored
            .into_iter()
            .map(|(item, score)| MemoryRef {
                source: item.source,
                key: item.key,
                content: item.content,
                score,
                queried_at_seq: 0,
            })
            .collect())
    }

    fn expire(&self, key: &str) -> Result<(), MemoryError> {
        let mut guard = self.items.lock().unwrap();
        if let Some(item) = guard.get_mut(key) {
            item.expires_at = "1970-01-01T00:00:00Z".into(); // 强制过期
        }
        Ok(())
    }
}

// ---------- helpers ----------

fn has_all_tags(item_tags: &[String], required: &[String]) -> bool {
    required.iter().all(|r| item_tags.iter().any(|t| t == r))
}

fn score_item(q: &Query, item: &MemoryItem) -> f64 {
    if !q.semantic.is_empty() && !item.embedding.is_empty() {
        let emb = fake_embed(&q.semantic, item.embedding.len());
        return cosine(&emb, &item.embedding);
    }
    if !q.keywords.is_empty() {
        let hits = q.keywords.iter()
            .filter(|kw| item.key.contains(kw.as_str()) || item.content.contains(kw.as_str()))
            .count();
        return hits as f64 / q.keywords.len() as f64;
    }
    1.0
}

fn cosine(a: &[f32], b: &[f32]) -> f64 {
    if a.is_empty() || a.len() != b.len() {
        return 0.0;
    }
    let mut dot = 0.0f64;
    let mut na = 0.0f64;
    let mut nb = 0.0f64;
    for i in 0..a.len() {
        dot += a[i] as f64 * b[i] as f64;
        na += a[i] as f64 * a[i] as f64;
        nb += b[i] as f64 * b[i] as f64;
    }
    if na == 0.0 || nb == 0.0 {
        return 0.0;
    }
    dot / (na.sqrt() * nb.sqrt())
}

fn fake_embed(text: &str, dim: usize) -> Vec<f32> {
    let mut out = vec![0.0f32; dim];
    if dim == 0 {
        return out;
    }
    for (i, ch) in text.chars().enumerate() {
        out[i % dim] += ch as u32 as f32 / 1000.0;
    }
    let mut norm = 0.0f64;
    for v in &out {
        norm += *v as f64 * *v as f64;
    }
    if norm > 0.0 {
        let s = (1.0 / norm.sqrt()) as f32;
        for v in out.iter_mut() {
            *v *= s;
        }
    }
    out
}

/// EmbedText 教学期用 fake_embed。生产替换为真 embedder。
pub fn embed_text(text: &str, dim: usize) -> Vec<f32> {
    fake_embed(text, dim)
}
