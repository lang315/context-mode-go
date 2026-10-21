package hookio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

// maxInput caps one hook payload (spec §1.1). A larger tool_response is not
// worth capturing, and the cap bounds memory.
const maxInput = 16 << 20

// ReadInput reads all of r. More than 16 MiB is an error rather than a
// silent truncation, which would hand the parser a broken object.
func ReadInput(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxInput {
		return nil, fmt.Errorf("hookio: input exceeds %d bytes", maxInput)
	}
	return b, nil
}

var transcriptUUID = regexp.MustCompile(`([a-f0-9-]{36})\.jsonl$`)

// SessionID resolves the session id as TS getSessionId does for Claude Code
// (spec §1.4). Empty strings fall through, as falsy values do in JS.
func SessionID(in *Object, getenv func(string) string, ppid int) string {
	if p, ok := in.String("transcript_path"); ok {
		if m := transcriptUUID.FindStringSubmatch(p); m != nil {
			return m[1]
		}
	}
	if s, ok := in.String("session_id"); ok && s != "" {
		return s
	}
	if s := getenv("CLAUDE_SESSION_ID"); s != "" {
		return s
	}
	return "pid-" + strconv.Itoa(ppid)
}

var safeFileID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// FileID makes sid safe to use in a file name: sid itself when it is short
// and plain, else the first 16 hex chars of its SHA-256. "." and ".." match
// the pattern but would name a directory, so they are hashed too.
func FileID(sid string) string {
	if safeFileID.MatchString(sid) && sid != "." && sid != ".." {
		return sid
	}
	sum := sha256.Sum256([]byte(sid))
	return hex.EncodeToString(sum[:])[:16]
}
