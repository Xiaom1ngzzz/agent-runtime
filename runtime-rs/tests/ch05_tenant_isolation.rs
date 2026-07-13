use agent_runtime_rs::memory::{InMemStore, MemoryItem, MemoryStore, Query};

#[test]
fn tenant_isolation() {
    let store = InMemStore::new();
    store
        .upsert(MemoryItem {
            key: "pref:unit".into(),
            content: "摄氏度".into(),
            tenant_id: "tenant-a".into(),
            version: 1,
            ..Default::default()
        })
        .expect("upsert a");
    store
        .upsert(MemoryItem {
            key: "pref:unit".into(),
            content: "华氏度".into(),
            tenant_id: "tenant-b".into(),
            version: 1,
            ..Default::default()
        })
        .expect("upsert b");

    let refs = store
        .query(&Query {
            keywords: vec!["度".into()],
            tenant_id: "tenant-a".into(),
            ..Default::default()
        })
        .expect("query a");
    assert_eq!(refs.len(), 1);
    assert_eq!(refs[0].content, "摄氏度");

    let refs = store
        .query(&Query {
            keywords: vec!["度".into()],
            tenant_id: "tenant-b".into(),
            ..Default::default()
        })
        .expect("query b");
    assert_eq!(refs.len(), 1);
    assert_eq!(refs[0].content, "华氏度");

    let err = store.upsert(MemoryItem {
        key: "pref:unit".into(),
        content: "泄漏".into(),
        tenant_id: "tenant-a".into(),
        version: 2,
        ..Default::default()
    });
    assert!(err.is_ok(), "same-tenant update should succeed: {:?}", err);

    let refs = store
        .query(&Query {
            keywords: vec!["泄漏".into()],
            tenant_id: "tenant-a".into(),
            ..Default::default()
        })
        .expect("query updated");
    assert_eq!(refs.len(), 1);
    assert_eq!(refs[0].content, "泄漏");
}
