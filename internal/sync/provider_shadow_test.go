package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourcePlansEffectsWithoutWriter(t *testing.T) {
	sourceErr := errors.New("bad session")
	provider := &shadowTestProvider{
		ProviderBase: parser.ProviderBase{
			Def: parser.AgentDef{
				Type:        parser.AgentCodex,
				DisplayName: "Codex",
			},
		},
		fingerprint: parser.SourceFingerprint{
			Key:     "source-key",
			Size:    123,
			MTimeNS: 456,
		},
		outcome: parser.ParseOutcome{
			Results: []parser.ParseResultOutcome{
				{
					Result: parser.ParseResult{
						Session: parser.ParsedSession{
							ID:    "codex:one",
							Agent: parser.AgentCodex,
						},
					},
					DataVersion: parser.DataVersionCurrent,
				},
				{
					Result: parser.ParseResult{
						Session: parser.ParsedSession{
							ID:    "codex:two",
							Agent: parser.AgentCodex,
						},
					},
					DataVersion: parser.DataVersionNeedsRetry,
					RetryReason: "fallback parser",
				},
			},
			ExcludedSessionIDs: []string{"codex:excluded"},
			SourceErrors: []parser.SourceError{
				{
					SourceKey:   "source-key",
					DisplayPath: "display-path",
					SessionID:   "codex:bad",
					Err:         sourceErr,
					Retryable:   true,
				},
			},
			SkipReason:   parser.SkipNonInteractive,
			ForceReplace: true,
		},
	}

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source: parser.SourceRef{
			Provider:       parser.AgentCodex,
			Key:            "source-key",
			DisplayPath:    "display-path",
			FingerprintKey: "fingerprint-key",
		},
		Machine:    "devbox",
		ForceParse: true,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
	assert.Equal(t, "devbox", provider.parseRequest.Machine)
	assert.True(t, provider.parseRequest.ForceParse)
	assert.Equal(t, int64(456), provider.parseRequest.Fingerprint.MTimeNS)

	require.Len(t, observation.Results, 2)
	assert.Equal(t, "codex:one", observation.Results[0].Session.ID)
	assert.Equal(t, []string{"codex:excluded"}, observation.ExcludedSessionIDs)
	assert.Equal(t, parser.SkipNonInteractive, observation.SkipReason)
	assert.True(t, observation.ForceReplace)

	assert.Equal(t, []string{"source-key"}, observation.Planned.SourceKeys)
	assert.Equal(t, []string{"fingerprint-key"}, observation.Planned.SkipCacheKeys)
	assert.Equal(t, []string{"codex:one", "codex:two"}, observation.Planned.DataVersionSessionIDs())
	assert.Equal(t, []string{"codex:two"}, observation.Planned.RetrySessionIDs())
	require.Len(t, observation.Planned.Diagnostics, 1)
	assert.Equal(t, "codex:bad", observation.Planned.Diagnostics[0].SessionID)
	assert.True(t, observation.Planned.Diagnostics[0].Retryable)
	assert.ErrorIs(t, observation.Planned.Diagnostics[0].Err, sourceErr)
	assert.Empty(t, observation.Planned.SSEScopes)
}

func TestObserveProviderSourceRejectsProviderMismatch(t *testing.T) {
	provider := &shadowTestProvider{
		ProviderBase: parser.ProviderBase{
			Def: parser.AgentDef{
				Type:        parser.AgentCodex,
				DisplayName: "Codex",
			},
		},
	}

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source: parser.SourceRef{
			Provider: parser.AgentClaude,
			Key:      "source-key",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(parser.AgentClaude))
	assert.Contains(t, err.Error(), string(parser.AgentCodex))
	assert.Empty(t, observation)
	assert.Empty(t, provider.calls)
}

func TestObserveProviderSourceRejectsCrossProviderResult(t *testing.T) {
	provider := &shadowTestProvider{
		ProviderBase: parser.ProviderBase{
			Def: parser.AgentDef{
				Type:        parser.AgentCodex,
				DisplayName: "Codex",
				IDPrefix:    "codex:",
			},
		},
		outcome: parser.ParseOutcome{
			Results: []parser.ParseResultOutcome{
				{
					Result: parser.ParseResult{
						Session: parser.ParsedSession{
							ID:    "codex:one",
							Agent: parser.AgentClaude,
						},
					},
				},
			},
		},
	}

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source: parser.SourceRef{
			Provider: parser.AgentCodex,
			Key:      "source-key",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session agent")
	assert.Contains(t, err.Error(), string(parser.AgentClaude))
	assert.Contains(t, err.Error(), string(parser.AgentCodex))
	assert.Empty(t, observation)
	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
}

func TestObserveProviderSourceRejectsForeignSessionID(t *testing.T) {
	provider := &shadowTestProvider{
		ProviderBase: parser.ProviderBase{
			Def: parser.AgentDef{
				Type:        parser.AgentCodex,
				DisplayName: "Codex",
				IDPrefix:    "codex:",
			},
		},
		outcome: parser.ParseOutcome{
			Results: []parser.ParseResultOutcome{
				{
					Result: parser.ParseResult{
						Session: parser.ParsedSession{
							ID:    "claude:one",
							Agent: parser.AgentCodex,
						},
					},
				},
			},
		},
	}

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source: parser.SourceRef{
			Provider: parser.AgentCodex,
			Key:      "source-key",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session id")
	assert.Contains(t, err.Error(), "claude:one")
	assert.Contains(t, err.Error(), "codex:")
	assert.Empty(t, observation)
	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
}

func TestObserveProviderSourceRejectsUnrelatedDiagnosticSourceKey(t *testing.T) {
	sourceErr := errors.New("bad source")
	provider := &shadowTestProvider{
		ProviderBase: parser.ProviderBase{
			Def: parser.AgentDef{
				Type:        parser.AgentCodex,
				DisplayName: "Codex",
				IDPrefix:    "codex:",
			},
		},
		fingerprint: parser.SourceFingerprint{
			Key: "fingerprint-key",
		},
		outcome: parser.ParseOutcome{
			SourceErrors: []parser.SourceError{
				{
					SourceKey: "other-source",
					SessionID: "codex:bad",
					Err:       sourceErr,
				},
			},
		},
	}

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source: parser.SourceRef{
			Provider:       parser.AgentCodex,
			Key:            "source-key",
			FingerprintKey: "source-fingerprint-key",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostic source key")
	assert.Contains(t, err.Error(), "other-source")
	assert.Empty(t, observation)
	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
}

func TestObserveProviderSourceStopsAfterFingerprintError(t *testing.T) {
	fingerprintErr := errors.New("stat failed")
	provider := &shadowTestProvider{
		ProviderBase: parser.ProviderBase{
			Def: parser.AgentDef{
				Type:        parser.AgentCodex,
				DisplayName: "Codex",
			},
		},
		fingerprintErr: fingerprintErr,
	}

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source: parser.SourceRef{
			Provider: parser.AgentCodex,
			Key:      "source-key",
		},
	})
	require.ErrorIs(t, err, fingerprintErr)
	assert.Empty(t, observation)
	assert.Equal(t, []string{"fingerprint"}, provider.calls)
}

type shadowTestProvider struct {
	parser.ProviderBase
	calls          []string
	fingerprint    parser.SourceFingerprint
	fingerprintErr error
	outcome        parser.ParseOutcome
	parseErr       error
	parseRequest   parser.ParseRequest
}

func (p *shadowTestProvider) Fingerprint(
	context.Context,
	parser.SourceRef,
) (parser.SourceFingerprint, error) {
	p.calls = append(p.calls, "fingerprint")
	if p.fingerprintErr != nil {
		return parser.SourceFingerprint{}, p.fingerprintErr
	}
	return p.fingerprint, nil
}

func (p *shadowTestProvider) Parse(
	_ context.Context,
	req parser.ParseRequest,
) (parser.ParseOutcome, error) {
	p.calls = append(p.calls, "parse")
	p.parseRequest = req
	if p.parseErr != nil {
		return parser.ParseOutcome{}, p.parseErr
	}
	return p.outcome, nil
}
