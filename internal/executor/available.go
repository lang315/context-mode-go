package executor

// Available lists the languages whose runtime is found, in schema order. It
// detects every runtime that has not been used yet (cmgo doctor's Runtimes).
func (e *Executor) Available() []Language {
	var out []Language
	for _, l := range Languages {
		if e.runtime(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
