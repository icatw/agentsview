package sync

import (
	"context"
	"fmt"
	"strings"

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
	if err := validateProviderOutcome(def, req.Source, fingerprint, outcome); err != nil {
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

func validateProviderOutcome(
	def parser.AgentDef,
	source parser.SourceRef,
	fingerprint parser.SourceFingerprint,
	outcome parser.ParseOutcome,
) error {
	for _, result := range outcome.Results {
		session := result.Result.Session
		if session.Agent != def.Type {
			return fmt.Errorf(
				"%s: provider result session agent mismatch for %q: got %s",
				def.Type,
				session.ID,
				session.Agent,
			)
		}
		if err := validateProviderParseResultSessionIDs(def, result.Result); err != nil {
			return err
		}
	}
	for _, sessionID := range outcome.ExcludedSessionIDs {
		if err := validateProviderSessionID(def, sessionID, "excluded session id"); err != nil {
			return err
		}
	}
	for _, sourceErr := range outcome.SourceErrors {
		if err := validateProviderSessionID(def, sourceErr.SessionID, "diagnostic session id"); err != nil {
			return err
		}
		if sourceErr.SourceKey == "" {
			return fmt.Errorf(
				"%s: provider diagnostic source key is required for source %q",
				def.Type,
				source.Key,
			)
		}
		if !providerSourceKeyMatches(source, fingerprint, sourceErr.SourceKey) {
			return fmt.Errorf(
				"%s: provider diagnostic source key %q is unrelated to source %q",
				def.Type,
				sourceErr.SourceKey,
				source.Key,
			)
		}
	}
	return nil
}

func validateProviderParseResultSessionIDs(def parser.AgentDef, result parser.ParseResult) error {
	sessionIDs := []struct {
		field string
		id    string
	}{
		{field: "result session id", id: result.Session.ID},
		{field: "parent session id", id: result.Session.ParentSessionID},
	}
	for _, sessionID := range sessionIDs {
		if err := validateProviderSessionID(def, sessionID.id, sessionID.field); err != nil {
			return err
		}
	}
	for _, usage := range result.Session.UsageEvents {
		if err := validateProviderSessionID(def, usage.SessionID, "session usage event session id"); err != nil {
			return err
		}
	}
	for _, usage := range result.UsageEvents {
		if err := validateProviderSessionID(def, usage.SessionID, "usage event session id"); err != nil {
			return err
		}
	}
	for _, message := range result.Messages {
		for _, toolCall := range message.ToolCalls {
			if err := validateProviderSessionID(def, toolCall.SubagentSessionID, "tool call subagent session id"); err != nil {
				return err
			}
			for _, event := range toolCall.ResultEvents {
				if err := validateProviderSessionID(def, event.SubagentSessionID, "tool result event subagent session id"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateProviderSessionID(def parser.AgentDef, sessionID, field string) error {
	if sessionID == "" || def.IDPrefix == "" {
		return nil
	}
	if strings.HasPrefix(sessionID, def.IDPrefix) {
		return nil
	}
	return fmt.Errorf(
		"%s: provider %s %q must use prefix %q",
		def.Type,
		field,
		sessionID,
		def.IDPrefix,
	)
}

func providerSourceKeyMatches(
	source parser.SourceRef,
	fingerprint parser.SourceFingerprint,
	sourceKey string,
) bool {
	if sourceKey == "" {
		return true
	}
	for _, candidate := range []string{fingerprint.Key, source.FingerprintKey, source.Key} {
		if candidate == "" {
			continue
		}
		if sourceKey == candidate || strings.HasPrefix(sourceKey, candidate+"#") ||
			strings.HasPrefix(sourceKey, candidate+"::") ||
			strings.HasPrefix(sourceKey, candidate+"|") {
			return true
		}
	}
	return false
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
