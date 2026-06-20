package sync

import (
	"context"
	"fmt"

	"go.kenn.io/agentsview/internal/parser"
)

// ProviderObserveRequest is the source-level shadow-parse input used while the
// legacy sync path remains authoritative.
type ProviderObserveRequest struct {
	Source     parser.SourceRef
	Machine    string
	ForceParse bool
}

// ProviderObservation is the normalized, side-effect-free provider outcome for
// one source.
type ProviderObservation struct {
	Fingerprint        parser.SourceFingerprint
	Results            []parser.ParseResult
	ExcludedSessionIDs []string
	SourceErrors       []parser.SourceError
	SkipReason         parser.SkipReason
	ForceReplace       bool
	Planned            ProviderPlannedEffects
}

// ProviderPlannedEffects describes writes the provider path would have made.
// Shadow mode compares these in memory; it does not receive live DB, skip-cache,
// diagnostic, or SSE writers.
type ProviderPlannedEffects struct {
	SourceKeys    []string
	DataVersions  []ProviderPlannedDataVersion
	SkipCacheKeys []string
	Diagnostics   []ProviderPlannedDiagnostic
	SSEScopes     []string
}

// ProviderPlannedDataVersion is an in-memory data-version write candidate.
type ProviderPlannedDataVersion struct {
	SessionID   string
	State       parser.DataVersionState
	RetryReason string
}

// ProviderPlannedDiagnostic is an in-memory parse diagnostic candidate.
type ProviderPlannedDiagnostic struct {
	SourceKey   string
	DisplayPath string
	SessionID   string
	Err         error
	Retryable   bool
}

// DataVersionSessionIDs returns the planned data-version session IDs in parse
// result order.
func (p ProviderPlannedEffects) DataVersionSessionIDs() []string {
	ids := make([]string, 0, len(p.DataVersions))
	for _, dataVersion := range p.DataVersions {
		ids = append(ids, dataVersion.SessionID)
	}
	return ids
}

// RetrySessionIDs returns sessions that need a future parse retry.
func (p ProviderPlannedEffects) RetrySessionIDs() []string {
	var ids []string
	for _, dataVersion := range p.DataVersions {
		if dataVersion.State == parser.DataVersionNeedsRetry {
			ids = append(ids, dataVersion.SessionID)
		}
	}
	return ids
}

// ObserveProviderSource fingerprints and parses a provider source without
// mutating persisted session state. It is the source-level comparison surface
// provider migration branches use before caller-level dual-run wiring exists.
func ObserveProviderSource(
	ctx context.Context,
	provider parser.Provider,
	req ProviderObserveRequest,
) (ProviderObservation, error) {
	def := provider.Definition()
	if req.Source.Provider != def.Type {
		return ProviderObservation{}, fmt.Errorf(
			"provider source mismatch: source is %s, provider is %s",
			req.Source.Provider,
			def.Type,
		)
	}

	fingerprint, err := provider.Fingerprint(ctx, req.Source)
	if err != nil {
		return ProviderObservation{}, err
	}
	outcome, err := provider.Parse(ctx, parser.ParseRequest{
		Source:      req.Source,
		Fingerprint: fingerprint,
		Machine:     req.Machine,
		ForceParse:  req.ForceParse,
	})
	if err != nil {
		return ProviderObservation{}, err
	}

	observation := ProviderObservation{
		Fingerprint:        fingerprint,
		Results:            parseOutcomeResults(outcome.Results),
		ExcludedSessionIDs: append([]string(nil), outcome.ExcludedSessionIDs...),
		SourceErrors:       append([]parser.SourceError(nil), outcome.SourceErrors...),
		SkipReason:         outcome.SkipReason,
		ForceReplace:       outcome.ForceReplace,
	}
	observation.Planned = planProviderEffects(req.Source, fingerprint, outcome)
	return observation, nil
}

func parseOutcomeResults(outcomes []parser.ParseResultOutcome) []parser.ParseResult {
	results := make([]parser.ParseResult, 0, len(outcomes))
	for _, outcome := range outcomes {
		results = append(results, outcome.Result)
	}
	return results
}

func planProviderEffects(
	source parser.SourceRef,
	fingerprint parser.SourceFingerprint,
	outcome parser.ParseOutcome,
) ProviderPlannedEffects {
	planned := ProviderPlannedEffects{}
	if sourceKey := plannedSourceKey(source, fingerprint); sourceKey != "" {
		planned.SourceKeys = append(planned.SourceKeys, sourceKey)
	}
	if outcome.SkipReason != parser.SkipNone {
		if skipKey := plannedSkipKey(source, fingerprint); skipKey != "" {
			planned.SkipCacheKeys = append(planned.SkipCacheKeys, skipKey)
		}
	}
	for _, result := range outcome.Results {
		if result.Result.Session.ID == "" ||
			result.DataVersion == parser.DataVersionUnspecified {
			continue
		}
		planned.DataVersions = append(planned.DataVersions, ProviderPlannedDataVersion{
			SessionID:   result.Result.Session.ID,
			State:       result.DataVersion,
			RetryReason: result.RetryReason,
		})
	}
	for _, sourceErr := range outcome.SourceErrors {
		planned.Diagnostics = append(planned.Diagnostics, ProviderPlannedDiagnostic{
			SourceKey:   sourceErr.SourceKey,
			DisplayPath: sourceErr.DisplayPath,
			SessionID:   sourceErr.SessionID,
			Err:         sourceErr.Err,
			Retryable:   sourceErr.Retryable,
		})
	}
	return planned
}

func plannedSourceKey(
	source parser.SourceRef,
	fingerprint parser.SourceFingerprint,
) string {
	if fingerprint.Key != "" {
		return fingerprint.Key
	}
	if source.FingerprintKey != "" {
		return source.FingerprintKey
	}
	return source.Key
}

func plannedSkipKey(
	source parser.SourceRef,
	fingerprint parser.SourceFingerprint,
) string {
	if source.FingerprintKey != "" {
		return source.FingerprintKey
	}
	return plannedSourceKey(source, fingerprint)
}
