package session

import (
	"strings"
	"unicode"
)

// ackWords are the words a message that only acknowledges is made of:
// thanks, closings and receipts. Words that carry a result or an answer
// ("done", "yes", "no", "LGTM", "fixed") are not among them: the agent
// that asked needs those to go on.
var ackWords = map[string]bool{
	"thanks": true, "thank": true, "thx": true, "ty": true, "you": true, "youre": true, "welcome": true,
	"np": true, "problem": true, "anytime": true, "cheers": true, "appreciated": true, "appreciate": true,
	"it": true, "much": true, "a": true, "lot": true, "so": true, "very": true, "again": true,
	"great": true, "perfect": true, "awesome": true, "nice": true, "cool": true, "excellent": true,
	"glad": true, "to": true, "help": true, "happy": true, "sounds": true, "good": true,
	"got": true, "ok": true, "okay": true, "noted": true, "ack": true, "acknowledged": true,
	"roger": true, "understood": true, "will": true, "do": true, "sgtm": true, "makes": true, "sense": true,
	"and": true, "for": true, "the": true, "that": true, "this": true, "update": true, "heads": true, "up": true,
}

// isAck reports whether text only acknowledges: a dozen words at most, no
// question, every word a thanks, a closing or a receipt ("Thanks, got it!",
// "You're welcome", "Sounds good, will do"). Mentions and emoji do not
// count as words.
func isAck(text string) bool {
	if strings.Contains(text, "?") {
		return false
	}
	words := 0
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == '!' || r == ';' || r == ':' || r == '-'
	}) {
		if strings.HasPrefix(f, "@") || strings.HasPrefix(f, "$") {
			continue // a mention
		}
		w := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, f)
		if w == "" {
			continue // an emoji or punctuation
		}
		if !ackWords[w] {
			return false
		}
		words++
	}
	return words > 0 && words <= 12
}
