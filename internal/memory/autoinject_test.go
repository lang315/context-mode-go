package memory

import (
	"strings"
	"testing"
)

func TestAutoInjectionNothingToInject(t *testing.T) {
	if got := AutoInjection(nil); got != "" {
		t.Fatalf("AutoInjection(nil) = %q", got)
	}
	if got := AutoInjection(evs([3]string{"file", "file_read", "/a.ts"})); got != "" {
		t.Fatalf("AutoInjection(file only) = %q", got)
	}
}

func TestAutoInjectionBudgetDropsIntent(t *testing.T) {
	rows := [][3]string{{"role", "role", strings.Repeat("r", 450)}}
	for i := 0; i < 10; i++ {
		rows = append(rows, [3]string{"skill", "skill", strings.Repeat("s", 200) + string(rune('a'+i))})
	}
	rows = append(rows, [3]string{"intent", "intent", "review"})
	got := AutoInjection(evs(rows...))
	if !strings.Contains(got, "<behavioral_directive>\n"+strings.Repeat("r", 400)+"\n</behavioral_directive>") {
		t.Fatalf("role not cut to 400 units:\n%s", got)
	}
	if !strings.Contains(got, "<active_skills>") || strings.Contains(got, "<session_mode>") {
		t.Fatalf("want skills and no intent once the budget is spent:\n%s", got)
	}
}
