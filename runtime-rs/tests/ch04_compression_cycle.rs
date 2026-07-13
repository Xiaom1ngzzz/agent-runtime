//! ch04 §4.10.2 端到端证据 —— Rust 版。与 `runtime-go/compressor/compression_cycle_test.go` 对齐。
//!
//! 场景 6 turns → Threshold=4 → 前 2 个 Turn 被压 → Assemble 出 <prior_summary>。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use agent_runtime_rs::compressor::{ByTurns, Compressor, ScriptedSummarizer};
use agent_runtime_rs::context::{ContextEngine, LayeredContextEngine};
use agent_runtime_rs::domain::{
    Budget, Decision, Event, EventPayload, LLMResponse, Message, PayloadContextCompressed,
    PayloadProgressUpdated, PayloadSessionOpened, PayloadTaskCreated, PayloadTurnStarted,
    PayloadUserSpoke, Progress, Step, StepKind, Summary, Tool, ToolCall,
};
use agent_runtime_rs::runtime::Runtime;
use agent_runtime_rs::state::{EventStore as _, State as _};

use fakes::{
    append_all, ContextEngineFake, EventStoreFake, ExecutorFake, LLMScript,
    PromptCompilerPassthrough, StateFake, ToolFn,
};

#[test]
fn ch04_compression_cycle() {
    // ---------- 搭建 Runtime + Compressor ----------
    let store = Arc::new(Mutex::new(EventStoreFake::new()));
    let state = Arc::new(Mutex::new(StateFake::new()));

    let mut tools: HashMap<String, ToolFn> = HashMap::new();
    tools.insert("weather".into(), Box::new(|_| Ok(r#"{"temp":26}"#.into())));
    let tool_descs = vec![Tool { name: "weather".into(), ..Default::default() }];

    let script: Vec<LLMResponse> = (1..=6)
        .map(|i| LLMResponse {
            assistant: Message { role: "assistant".into(), ..Default::default() },
            tool_calls: vec![ToolCall {
                id: format!("c{}", i),
                name: "weather".into(),
                arguments: r#"{"city":"BJ"}"#.into(),
            }],
            tokens_in: 100,
            tokens_out: 20,
        })
        .collect();

    let rt = Runtime {
        event_store: store.clone(),
        state: state.clone(),
        context: Arc::new(ContextEngineFake::new(state.clone(), store.clone(), tool_descs.clone())),
        prompt: Arc::new(PromptCompilerPassthrough),
        llm: Arc::new(LLMScript::new(script)),
        executor: Arc::new(ExecutorFake::new(store.clone(), tools)),
    };

    let layered = LayeredContextEngine {
        state: state.clone(),
        store: Some(store.clone()),
        instructions: "You are an agent.".into(),
        tools: tool_descs,
    };

    let summarizer = ScriptedSummarizer::new(vec![Summary {
        user_intents: vec!["查北京天气(重复调用示范)".into()],
        tool_results: {
            let mut m = HashMap::new();
            m.insert("weather:BJ".into(), r#"{"temp":26}"#.into());
            m
        },
        decisions_made: vec![Decision {
            what: "统一用摄氏度".into(),
            why: "用户偏好".into(),
            at_seq: 2,
        }],
        open_questions: vec!["是否需要湿度数据".into()],
        model_used: "test-model".into(),
        prompt_version: "v1".into(),
        confidence: 0.85,
        ..Default::default()
    }]);
    let mut comp = ByTurns {
        state: state.clone(),
        store: store.clone(),
        summarizer: Box::new(summarizer),
        threshold: 4,
    };

    // ---------- Session / Task ----------
    let sid = "s1";
    let tid = "t1";
    append_all(&rt, sid, "", "", vec![
        EventPayload::SessionOpened(PayloadSessionOpened { principal: "u".into(), ..Default::default() }),
        EventPayload::UserSpoke(PayloadUserSpoke { text: "帮我盯天气".into() }),
    ]);
    append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskCreated(PayloadTaskCreated {
            goal: "盯天气".into(),
            budget: Budget { max_tokens: 8000, ..Default::default() },
        }),
    ]);

    // ---------- 跑 6 个 Turn 期间尝试压 ----------
    let mut compression_happened = false;
    for (i, turn_id) in ["r1", "r2", "r3", "r4", "r5", "r6"].iter().enumerate() {
        append_all(&rt, sid, tid, turn_id, vec![
            EventPayload::TurnStarted(PayloadTurnStarted { index: i as i32 }),
        ]);
        rt.step(sid, tid, turn_id).expect("step");
        let events = comp.tick(sid).expect("tick");
        if !events.is_empty() {
            let mut buf = events;
            {
                let mut s = store.lock().unwrap();
                s.append(&mut buf).unwrap();
            }
            {
                let mut st = state.lock().unwrap();
                st.apply(&buf).unwrap();
            }
            for e in &buf {
                if matches!(e.payload, EventPayload::ContextCompressed(_)) {
                    compression_happened = true;
                }
            }
        }
    }
    assert!(compression_happened, "expected at least one ContextCompressed event");
    append_all(&rt, sid, tid, "", vec![
        EventPayload::ProgressUpdated(PayloadProgressUpdated {
            task_id: tid.into(),
            progress: Progress {
                goal: "盯天气".into(),
                version: 1,
                updated_at: "after-r6".into(),
                done: vec![Step {
                    intent: "查询天气".into(),
                    action: "weather".into(),
                    observation: "temp=26".into(),
                    kind: StepKind::ToolCall,
                    ..Default::default()
                }],
                next: vec![Step {
                    intent: "汇总结果".into(),
                    action: "respond".into(),
                    kind: StepKind::Decision,
                    ..Default::default()
                }],
                ..Default::default()
            },
        }),
    ]);

    // ---------- 断言 1:View 里有 Summary + 部分 TurnDigest 被 Superseded ----------
    let view = state.lock().unwrap().view(sid).unwrap();
    assert!(!view.summaries.is_empty(), "summaries should be non-empty");
    let superseded_count = view.working_set.iter().filter(|d| d.superseded).count();
    assert!(superseded_count > 0, "expected at least one Superseded TurnDigest");

    // ---------- 断言 2:LayeredContextEngine.Assemble 输出含 <prior_summary> ----------
    let ctx = layered.assemble(sid, tid).unwrap();
    let has_summary = ctx
        .messages
        .iter()
        .any(|m| m.content.contains("<prior_summary>"));
    assert!(has_summary, "Assemble output should contain <prior_summary>");
    let has_progress = ctx.messages.iter().any(|m| m.content.contains("<task_progress"));
    assert!(has_progress, "Assemble output should contain <task_progress>");

    // ---------- 断言 3:回放性 —— 全量 Fold 后视图相同 ----------
    let all_events: Vec<Event> = store.lock().unwrap().snapshot();
    let mut fresh = StateFake::new();
    fresh.apply(&all_events).unwrap();
    let view2 = fresh.view(sid).unwrap();

    assert_eq!(view.summaries.len(), view2.summaries.len(), "summaries len mismatch");
    assert_eq!(view.working_set.len(), view2.working_set.len(), "working_set len mismatch");
    for i in 0..view.working_set.len() {
        assert_eq!(
            view.working_set[i].superseded,
            view2.working_set[i].superseded,
            "superseded mismatch at {}", i
        );
    }

    // 静默使用一个 PayloadContextCompressed 引用,让 lint 不抱怨
    let _ = std::marker::PhantomData::<PayloadContextCompressed>;
}
