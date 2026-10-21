package routing

import "strings"

// The TS regexes of the Bash redirects (routing.mjs:733-818), in RE2 form.
// (?i) folds U+017F and U+212A to s and k, which JS /i without u does not;
// no TS example depends on it.
var (
	curlTrigger  = jsRegexp(`(?i)(^|\s|&&|\||;)(curl|wget)\s`)
	chainSplit   = jsRegexp(`\s*(?:&&|\|\||;)\s*`)
	curlSegment  = jsRegexp(`(?i)(^|\s)(curl|wget)\s`)
	curlWord     = jsRegexp(`(?i)\bcurl\b`)
	wgetWord     = jsRegexp(`(?i)\bwget\b`)
	curlOutput   = jsRegexp(`\s(-o|--output)\s`)
	wgetOutput   = jsRegexp(`\s(-O|--output-document)\s`)
	curlStdout   = jsRegexp(`\s(-o|--output)\s+(-|/dev/stdout)(\s|$)`)
	wgetStdout   = jsRegexp(`\s(-O|--output-document)\s+(-|/dev/stdout)(\s|$)`)
	verboseFlag  = jsRegexp(`\s(-v|--verbose|--trace|-D\s+-)\b`) // RE2 \b is ASCII, like JS
	curlSilent   = jsRegexp(`\s-[a-zA-Z]*s|--silent`)
	wgetQuiet    = jsRegexp(`\s-[a-zA-Z]*q|--quiet`)
	inlineFetch  = jsRegexp(`(?i)fetch\s*\(\s*['"](https?://|http)`)
	inlineReqs   = jsRegexp(`(?i)requests\.(get|post|put)\s*\(`)
	inlineHTTPGo = jsRegexp(`(?i)http\.(get|request)\s*\(`)
	buildCommand = jsRegexp(`(?i)(^|\s|&&|\||;)(\./gradlew|gradlew|gradle|\./mvnw|mvnw|mvn|\./sbt|sbt)(\s|$)`)
)

// curlCheck ports the curl/wget block on the quote-stripped command. found
// means the trigger matched; dangerous means some chained segment can flood
// the context. found && !dangerous makes TS return null at once, skipping
// every later Bash check (R1 P6).
func curlCheck(stripped string) (found, dangerous bool) {
	if !curlTrigger.MatchString(stripped) {
		return false, false
	}
	for _, seg := range chainSplit.Split(stripped, -1) {
		if curlSegmentDangerous(jsTrim(seg)) {
			return true, true
		}
	}
	return true, false
}

// curlSegmentDangerous is the per-segment test of routing.mjs:738-767. Any
// '>' counts as file output (R1 P5), so curl -s URL 2>/dev/null is safe.
func curlSegmentDangerous(s string) bool {
	if !curlSegment.MatchString(s) {
		return false
	}
	isCurl := curlWord.MatchString(s)
	isWget := wgetWord.MatchString(s)
	output, silent := wgetOutput, wgetQuiet
	if isCurl {
		output, silent = curlOutput, curlSilent
	}
	if !output.MatchString(s) && !strings.Contains(s, ">") {
		return true
	}
	if isCurl && curlStdout.MatchString(s) || isWget && wgetStdout.MatchString(s) {
		return true
	}
	if verboseFlag.MatchString(s) {
		return true
	}
	return !silent.MatchString(s)
}

// inlineHTTP ports routing.mjs:793-798 on the heredoc-stripped command
// (quotes kept, so code passed by -e or -c is seen).
func inlineHTTP(noHeredoc string) bool {
	return inlineFetch.MatchString(noHeredoc) || inlineReqs.MatchString(noHeredoc) || inlineHTTPGo.MatchString(noHeredoc)
}

// buildTool ports routing.mjs:810 on the quote-stripped command.
func buildTool(stripped string) bool {
	return buildCommand.MatchString(stripped)
}
