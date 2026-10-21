package capture

import (
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

func TestDecisionEvents(t *testing.T) {
	cases := []struct{ name, input, resp, want string }{
		{"CC object response", `{"questions":[{"question":"Which DB?","options":[]}]}`,
			`{"questions":[],"answers":{"Which DB?":"SQLite"}}`, "Q: Which DB? → A: SQLite"},
		{"string response", `{"questions":[{"question":"Which DB?"}]}`,
			`"{\"answers\":{\"Which DB?\":\"SQLite\"}}"`, "Q: Which DB? → A: SQLite"},
		{"multi-select keeps strings", `{"questions":[{"question":"Pick?"}]}`,
			`{"answers":{"Pick?":["a",1,"b"]}}`, "Q: Pick? → A: a | b"},
		{"fallback joins non-empty answers", `{"questions":[{"question":"Q1"}]}`,
			`{"answers":{"other":"x","empty":"","list":["y"]}}`, "Q: Q1 → A: x | y"},
		{"answers array", `{"questions":[{"question":"Q1"}]}`,
			`{"answers":["x",["y","z"]]}`, "Q: Q1 → A: x | y | z"},
		{"non-JSON response", `{"questions":[{"question":"Q"}]}`, `"not json"`, "Q: Q → A: "},
		{"no questions", `{}`, `{"answers":{"a":"b"}}`, "answer: b"},
		{"question not an object", `{"questions":["Q"]}`, `{"answers":{"a":"b"}}`, "answer: b"},
	}
	for _, c := range cases {
		got := decisionEvents(newCall(t, "AskUserQuestion", c.input, c.resp))
		checkEvents(t, c.name, got, []store.Event{ev("decision_question", "decision", 2, c.want)})
	}
	checkEvents(t, "other tool", decisionEvents(newCall(t, "Bash", `{}`, `"x"`)), nil)
}
