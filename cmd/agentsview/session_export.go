// ABOUTME: `session export <id>` subcommand — streams the raw source
// ABOUTME: JSONL file for a locally-synced session. Local-only by
// ABOUTME: design; bypasses the SessionService layer.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.kenn.io/agentsview/internal/config"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/parser"
)

func newSessionExportCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "export <id>",
		Short:        "Stream the raw source JSONL for a session (local only)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("server") {
				return fmt.Errorf(
					"session export: local-only command; --server not supported",
				)
			}
			if cmd.Flags().Changed("format") {
				return fmt.Errorf(
					"session export: streams raw bytes; --format not supported",
				)
			}
			if pgReadRequested(cmd) {
				return fmt.Errorf(
					"session export: local-only command; --pg not supported",
				)
			}
			cfg, err := config.LoadPFlags(cmd.Flags())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			applyClassifierConfig(cfg)
			d, err := db.Open(cfg.DBPath)
			if err != nil {
				return fmt.Errorf("open local archive: %w", err)
			}
			defer d.Close()

			id, err := resolveSessionID(cmd.Context(), d, args[0])
			if err != nil {
				return err
			}
			if id == "" {
				return fmt.Errorf(
					"session not in local archive: %s", args[0],
				)
			}
			sourcePath := sessionExportSourcePath(
				cmd.Context(), cfg, d, id,
			)
			if sourcePath == "" {
				return fmt.Errorf(
					"source file not found for session %s", id,
				)
			}
			// A Visual Studio Copilot trace file holds spans for several
			// conversations, so streaming the whole file would disclose
			// unrelated conversations. Filter to the requested conversation.
			if tracePath, conversationID, ok :=
				parser.ParseVisualStudioCopilotVirtualPath(sourcePath); ok {
				err := parser.WriteVisualStudioCopilotConversationJSONL(
					cmd.OutOrStdout(), tracePath, conversationID,
				)
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf(
						"source file not found: %s", tracePath,
					)
				}
				return err
			}
			if conversationID, ok :=
				sessionExportVisualStudioCopilotConversationID(id); ok &&
				parser.IsVisualStudioCopilotTraceFile(sourcePath) {
				err := parser.WriteVisualStudioCopilotConversationJSONL(
					cmd.OutOrStdout(), sourcePath, conversationID,
				)
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf(
						"source file not found: %s", sourcePath,
					)
				}
				return err
			}
			path := parser.ResolveSourceFilePath(sourcePath)
			f, err := os.Open(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf(
						"source file not found: %s", path,
					)
				}
				return err
			}
			defer f.Close()
			_, err = io.Copy(cmd.OutOrStdout(), f)
			return err
		},
	}
}

func sessionExportVisualStudioCopilotConversationID(
	sessionID string,
) (string, bool) {
	def, ok := parser.AgentByPrefix(sessionID)
	if !ok || def.Type != parser.AgentVSCopilot {
		return "", false
	}
	_, rawID := parser.StripHostPrefix(sessionID)
	rawID = strings.TrimPrefix(rawID, def.IDPrefix)
	return rawID, parser.IsValidSessionID(rawID)
}

func sessionExportSourcePath(
	ctx context.Context,
	cfg config.Config,
	database *db.DB,
	sessionID string,
) string {
	storedPath := database.GetSessionFilePath(sessionID)
	providerPath := sessionExportProviderSourcePath(
		ctx, cfg, sessionID, storedPath,
	)
	if providerPath != "" {
		return providerPath
	}
	return storedPath
}

func sessionExportProviderSourcePath(
	ctx context.Context,
	cfg config.Config,
	sessionID string,
	storedPath string,
) string {
	if host, _ := parser.StripHostPrefix(sessionID); host != "" {
		return ""
	}
	def, ok := parser.AgentByPrefix(sessionID)
	if !ok || !def.FileBased {
		return ""
	}
	factory, ok := parser.ProviderFactoryByType(def.Type)
	if !ok ||
		factory.Capabilities().Source.FindSource != parser.CapabilitySupported {
		return ""
	}
	roots := cfg.ResolveDirs(def.Type)
	if len(roots) == 0 {
		return ""
	}
	provider := factory.NewProvider(parser.ProviderConfig{
		Roots: roots,
	})
	_, rawID := parser.StripHostPrefix(sessionID)
	rawID = strings.TrimPrefix(rawID, def.IDPrefix)
	source, found, err := sessionExportFindProviderSource(
		ctx, provider, rawID, sessionID, storedPath,
	)
	if err != nil {
		log.Printf(
			"%s provider export source lookup for %s: %v",
			def.Type, sessionID, err,
		)
		return ""
	}
	if !found {
		return ""
	}
	return sessionExportProviderDiscoveredPath(source)
}

func sessionExportFindProviderSource(
	ctx context.Context,
	provider parser.Provider,
	rawID string,
	fullID string,
	storedPath string,
) (parser.SourceRef, bool, error) {
	req := parser.FindSourceRequest{
		RawSessionID:       rawID,
		FullSessionID:      fullID,
		StoredFilePath:     storedPath,
		FingerprintKey:     storedPath,
		RequireFreshSource: true,
	}
	source, found, err := provider.FindSource(ctx, req)
	if err != nil || found || storedPath == "" {
		return source, found, err
	}
	req.StoredFilePath = ""
	req.FingerprintKey = ""
	return provider.FindSource(ctx, req)
}

func sessionExportProviderDiscoveredPath(source parser.SourceRef) string {
	for _, path := range []string{
		source.DisplayPath,
		source.FingerprintKey,
		source.Key,
	} {
		if path != "" {
			return path
		}
	}
	return ""
}
