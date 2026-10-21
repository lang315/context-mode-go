package capture

import (
	"encoding/json"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// decisionEvents ports extractDecision (X:1096-1152): the first question and
// its answer from the response's answers map.
func decisionEvents(c call) []store.Event {
	if c.tool != "AskUserQuestion" {
		return nil
	}
	question := ""
	if raw, ok := c.input.Get("questions"); ok {
		var qs []json.RawMessage
		if json.Unmarshal(raw, &qs) == nil && len(qs) > 0 {
			if q, err := hookio.ParseObject(qs[0]); err == nil {
				question = inputString(q, "question", "")
			}
		}
	}
	answer := answerText(c.resp, question)
	data := "answer: " + answer
	if question != "" {
		data = "Q: " + question + " → A: " + answer
	}
	return []store.Event{ev("decision_question", "decision", 2, data)}
}

// answerText is the answer to question, else every non-empty answer joined
// with " | ". A response that is not JSON gives "".
// shortcut: Object.values puts integer-like keys first; insertion order is used. Upgrade if answers get numeric keys.
func answerText(resp, question string) string {
	top, err := hookio.ParseObject([]byte(resp))
	if err != nil {
		return ""
	}
	raw, ok := top.Get("answers")
	if !ok {
		return ""
	}
	var values []json.RawMessage
	if answers, ok := top.Object("answers"); ok {
		if v, ok := answers.Get(question); ok && question != "" {
			if s := answerValue(v); s != "" {
				return s
			}
		}
		for _, k := range answers.Keys() {
			v, _ := answers.Get(k)
			values = append(values, v)
		}
	} else if json.Unmarshal(raw, &values) != nil {
		return "" // a string or number has no answers
	}
	var out []string
	for _, v := range values {
		if s := answerValue(v); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " | ")
}

// answerValue is toAnswerText: a string, or the strings of an array joined.
func answerValue(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var out []string
	for _, it := range items {
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
		}
	}
	return strings.Join(out, " | ")
}
