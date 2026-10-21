package mcpserver

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func TestServerRegistersSixTools(t *testing.T) {
	h := newHarness(t, nil)
	res, err := h.Session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"ctx_batch_execute", "ctx_execute", "ctx_execute_file", "ctx_fetch_and_index", "ctx_purge", "ctx_search"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}
