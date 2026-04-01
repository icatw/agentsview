package db

import "strings"

// automatedPrefixes are first_message prefixes that identify
// automated (roborev) sessions. Matched case-sensitively.
var automatedPrefixes = []string{
	"You are a code reviewer.",
	"You are a security code reviewer.",
	"You are a design reviewer.",
	"You are a code assistant. Your task is to address",
	"## Analysis Request",
	"You are a code review insights analyst.",
	"# Fix Request\n",
}

// automatedSubstrings are patterns matched anywhere in the
// first message. Used for catch-all markers embedded in
// longer prompts.
var automatedSubstrings = []string{
	"invoked by roborev to perform this review",
}

// IsAutomatedSession returns true if the first message
// matches a known automated review/fix prompt pattern.
func IsAutomatedSession(firstMessage string) bool {
	for _, prefix := range automatedPrefixes {
		if strings.HasPrefix(firstMessage, prefix) {
			return true
		}
	}
	for _, sub := range automatedSubstrings {
		if strings.Contains(firstMessage, sub) {
			return true
		}
	}
	return false
}
