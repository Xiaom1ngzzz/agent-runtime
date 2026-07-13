package memory_test

import (
	stdctx "context"
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/memory"
)

func TestTenantIsolation(t *testing.T) {
	ctx := stdctx.Background()
	store := memory.NewInMemStore()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	must(store.Upsert(ctx, domain.MemoryItem{
		Key: "pref:unit", Content: "摄氏度", TenantID: "tenant-a", Version: 1,
	}))
	must(store.Upsert(ctx, domain.MemoryItem{
		Key: "pref:unit", Content: "华氏度", TenantID: "tenant-b", Version: 1,
	}))

	refs, err := store.Query(ctx, domain.Query{Keywords: []string{"度"}, TenantID: "tenant-a"})
	must(err)
	if len(refs) != 1 || refs[0].Content != "摄氏度" {
		t.Fatalf("tenant-a query: %+v", refs)
	}

	refs, err = store.Query(ctx, domain.Query{Keywords: []string{"度"}, TenantID: "tenant-b"})
	must(err)
	if len(refs) != 1 || refs[0].Content != "华氏度" {
		t.Fatalf("tenant-b query: %+v", refs)
	}

	err = store.Upsert(ctx, domain.MemoryItem{
		Key: "pref:unit", Content: "泄漏", TenantID: "tenant-a", Version: 2,
	})
	if err != nil {
		t.Fatalf("same-tenant update should succeed: %v", err)
	}
	refs, err = store.Query(ctx, domain.Query{Keywords: []string{"泄漏"}, TenantID: "tenant-a"})
	must(err)
	if len(refs) != 1 || refs[0].Content != "泄漏" {
		t.Fatalf("updated tenant-a item: %+v", refs)
	}
}
