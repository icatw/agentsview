package db

import "testing"

func TestIsAutomatedSession(t *testing.T) {
	tests := []struct {
		name         string
		firstMessage string
		want         bool
	}{
		{"EmptyMessage", "", false},
		{"NormalUserPrompt", "fix the login bug", false},

		// Code review
		{
			"CodeReviewFull",
			"You are a code reviewer. Review the code changes shown below.\n\n## Changes",
			true,
		},
		{
			"CodeReviewShort",
			"You are a code reviewer. Here is a diff.",
			true,
		},

		// Security review
		{
			"SecurityReview",
			"You are a security code reviewer. Analyze the following.",
			true,
		},

		// Design review
		{
			"DesignReview",
			"You are a design reviewer. Review the architectural changes.",
			true,
		},

		// Fix (code assistant)
		{
			"CodeAssistantFix",
			"You are a code assistant. Your task is to address the following findings.",
			true,
		},

		// Analysis request
		{
			"AnalysisRequest",
			"## Analysis Request\n\nPlease analyze the following code.",
			true,
		},

		// Insights analyst
		{
			"InsightsAnalyst",
			"You are a code review insights analyst. Summarize trends.",
			true,
		},

		// Legacy fix request
		{
			"FixRequestWithBody",
			"# Fix Request\nAn analysis was performed.",
			true,
		},
		{
			"FixRequestNoNewline",
			"# Fix Request",
			false,
		},
		{
			"FixRequestUserHeading",
			"# Fix Request for login flow",
			false,
		},

		// Catch-all substring
		{
			"RoborevSubstringInMiddle",
			"IMPORTANT: You are being invoked by roborev to perform this review directly.\n\nReview the diff.",
			true,
		},

		// Negative cases
		{
			"SimilarButNotReview",
			"You are a code reviewer but I need help",
			false,
		},
		{
			"NormalFix",
			"Fix the request handler",
			false,
		},
		{
			"AnalysisInBody",
			"Please do an ## Analysis Request of this code",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAutomatedSession(tt.firstMessage)
			if got != tt.want {
				t.Errorf(
					"IsAutomatedSession(%q) = %v, want %v",
					tt.firstMessage, got, tt.want,
				)
			}
		})
	}
}
