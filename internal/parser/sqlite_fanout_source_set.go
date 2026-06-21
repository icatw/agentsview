package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SQLiteFanoutSessionMeta describes one logical session inside a shared SQLite
// database source.
type SQLiteFanoutSessionMeta struct {
	SessionID   string
	VirtualPath string
	FileMtime   int64
}

// SQLiteFanoutSourceSetOptions configures SQLite fan-out source discovery.
type SQLiteFanoutSourceSetOptions struct {
	DBName   string
	FindDB   func(root string) string
	ListMeta func(dbPath string) ([]SQLiteFanoutSessionMeta, error)
}

// SQLiteFanoutSource is the in-memory payload stored in SQLite fan-out
// SourceRefs.
type SQLiteFanoutSource struct {
	Root      string
	DBPath    string
	SessionID string
}

// SQLiteFanoutSourceSet discovers one SourceRef per logical session inside a
// shared SQLite database file.
type SQLiteFanoutSourceSet struct {
	provider AgentType
	roots    []string
	options  SQLiteFanoutSourceSetOptions
}

func NewSQLiteFanoutSourceSet(
	provider AgentType,
	roots []string,
	options SQLiteFanoutSourceSetOptions,
) SQLiteFanoutSourceSet {
	return SQLiteFanoutSourceSet{
		provider: provider,
		roots:    cleanJSONLRoots(roots),
		options:  options,
	}
}

func (s SQLiteFanoutSourceSet) Discover(ctx context.Context) ([]SourceRef, error) {
	var sources []SourceRef
	seen := make(map[string]struct{})
	for _, root := range s.roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dbPath := s.findDB(root)
		if dbPath == "" {
			continue
		}
		metas, err := s.listMeta(dbPath)
		if err != nil {
			return nil, err
		}
		for _, meta := range metas {
			addJSONLSource(s.newSourceRef(root, dbPath, meta), &sources, seen)
		}
	}
	sortJSONLSources(sources)
	return sources, nil
}

func (s SQLiteFanoutSourceSet) WatchPlan(context.Context) (WatchPlan, error) {
	roots := make([]WatchRoot, 0, len(s.roots))
	for _, root := range s.roots {
		roots = append(roots, WatchRoot{
			Path:         root,
			Recursive:    false,
			IncludeGlobs: []string{s.options.DBName, s.options.DBName + "-*"},
			DebounceKey:  string(s.provider) + ":sqlite:" + root,
		})
	}
	return WatchPlan{Roots: roots}, nil
}

func (s SQLiteFanoutSourceSet) SourcesForChangedPath(
	ctx context.Context,
	req ChangedPathRequest,
) ([]SourceRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, root := range s.roots {
		if req.WatchRoot != "" && !samePath(req.WatchRoot, root) {
			continue
		}
		if ref, ok := s.sourceRef(root, req.Path, true); ok {
			return []SourceRef{ref}, nil
		}
		dbPath, ok := s.dbPathForEvent(root, req.Path)
		if !ok {
			continue
		}
		metas, err := s.listMeta(dbPath)
		if err != nil {
			return nil, err
		}
		sources := make([]SourceRef, 0, len(metas))
		seen := make(map[string]struct{}, len(metas))
		for _, meta := range metas {
			addJSONLSource(s.newSourceRef(root, dbPath, meta), &sources, seen)
		}
		for _, storedPath := range req.StoredSourcePaths {
			ref, ok := s.sourceRef(root, storedPath, true)
			if !ok {
				continue
			}
			src := ref.Opaque.(SQLiteFanoutSource)
			if samePath(src.DBPath, dbPath) {
				addJSONLSource(ref, &sources, seen)
			}
		}
		sortJSONLSources(sources)
		return sources, nil
	}
	return nil, nil
}

func (s SQLiteFanoutSourceSet) FindSource(
	ctx context.Context,
	req FindSourceRequest,
) (SourceRef, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceRef{}, false, err
	}
	freshStoredSource := req.RequireFreshSource &&
		(req.StoredFilePath != "" || req.FingerprintKey != "")
	for _, path := range []string{req.StoredFilePath, req.FingerprintKey} {
		if path == "" {
			continue
		}
		for _, root := range s.roots {
			source, ok := s.sourceRef(root, path, true)
			if !ok {
				continue
			}
			src := source.Opaque.(SQLiteFanoutSource)
			if req.RawSessionID != "" && src.SessionID != req.RawSessionID {
				continue
			}
			if req.RequireFreshSource {
				fresh, err := s.sourceExists(src)
				if err != nil {
					return SourceRef{}, false, err
				}
				if !fresh {
					continue
				}
			}
			return source, true, nil
		}
	}
	if freshStoredSource || req.RawSessionID == "" {
		return SourceRef{}, false, nil
	}
	for _, root := range s.roots {
		dbPath := s.findDB(root)
		if dbPath == "" {
			continue
		}
		metas, err := s.listMeta(dbPath)
		if err != nil {
			return SourceRef{}, false, err
		}
		for _, meta := range metas {
			if meta.SessionID == req.RawSessionID {
				return s.newSourceRef(root, dbPath, meta), true, nil
			}
		}
	}
	return SourceRef{}, false, nil
}

func (s SQLiteFanoutSourceSet) Fingerprint(
	ctx context.Context,
	source SourceRef,
) (SourceFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return SourceFingerprint{}, err
	}
	src, ok := s.sourceFromRef(source)
	if !ok {
		return SourceFingerprint{}, fmt.Errorf("%s sqlite fan-out source path unavailable", s.provider)
	}
	key := firstNonEmptyJSONLString(source.FingerprintKey, source.Key, src.virtualPath())
	if _, err := os.Stat(src.DBPath); err != nil {
		if os.IsNotExist(err) {
			return SourceFingerprint{Key: key}, nil
		}
		return SourceFingerprint{}, fmt.Errorf("stat %s: %w", src.DBPath, err)
	}
	metas, err := s.listMeta(src.DBPath)
	if err != nil {
		return SourceFingerprint{}, err
	}
	for _, meta := range metas {
		if meta.SessionID == src.SessionID {
			return SourceFingerprint{Key: key, MTimeNS: meta.FileMtime}, nil
		}
	}
	return SourceFingerprint{Key: key}, nil
}

func (s SQLiteFanoutSourceSet) sourceFromRef(
	source SourceRef,
) (SQLiteFanoutSource, bool) {
	switch src := source.Opaque.(type) {
	case SQLiteFanoutSource:
		return src, src.DBPath != "" && src.SessionID != ""
	case *SQLiteFanoutSource:
		if src != nil && src.DBPath != "" && src.SessionID != "" {
			return *src, true
		}
	}
	for _, candidate := range []string{source.DisplayPath, source.FingerprintKey, source.Key} {
		for _, root := range s.roots {
			if ref, ok := s.sourceRef(root, candidate, true); ok {
				return ref.Opaque.(SQLiteFanoutSource), true
			}
		}
	}
	return SQLiteFanoutSource{}, false
}

func (s SQLiteFanoutSourceSet) sourceExists(src SQLiteFanoutSource) (bool, error) {
	if !IsRegularFile(src.DBPath) {
		return false, nil
	}
	metas, err := s.listMeta(src.DBPath)
	if err != nil {
		return false, err
	}
	for _, meta := range metas {
		if meta.SessionID == src.SessionID {
			return true, nil
		}
	}
	return false, nil
}

func (s SQLiteFanoutSourceSet) sourceRef(
	root, path string,
	allowMissing bool,
) (SourceRef, bool) {
	root = filepath.Clean(root)
	dbPath, sessionID, ok := ParseVirtualSourcePathForBase(path, s.options.DBName)
	if !ok || !samePath(dbPath, filepath.Join(root, s.options.DBName)) {
		return SourceRef{}, false
	}
	if !allowMissing && !IsRegularFile(dbPath) {
		return SourceRef{}, false
	}
	return s.newSourceRef(root, dbPath, SQLiteFanoutSessionMeta{
		SessionID:   sessionID,
		VirtualPath: VirtualSourcePath(dbPath, sessionID),
	}), true
}

func (s SQLiteFanoutSourceSet) dbPathForEvent(root, path string) (string, bool) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if !samePath(filepath.Dir(path), root) {
		return "", false
	}
	base := filepath.Base(path)
	if base == s.options.DBName || strings.HasPrefix(base, s.options.DBName+"-") {
		return filepath.Join(root, s.options.DBName), true
	}
	return "", false
}

func (s SQLiteFanoutSourceSet) newSourceRef(
	root, dbPath string,
	meta SQLiteFanoutSessionMeta,
) SourceRef {
	virtualPath := firstNonEmptyJSONLString(
		meta.VirtualPath,
		VirtualSourcePath(dbPath, meta.SessionID),
	)
	return SourceRef{
		Provider:       s.provider,
		Key:            virtualPath,
		DisplayPath:    virtualPath,
		FingerprintKey: virtualPath,
		Opaque: SQLiteFanoutSource{
			Root:      root,
			DBPath:    dbPath,
			SessionID: meta.SessionID,
		},
	}
}

func (s SQLiteFanoutSourceSet) findDB(root string) string {
	if s.options.FindDB == nil {
		return ""
	}
	return s.options.FindDB(root)
}

func (s SQLiteFanoutSourceSet) listMeta(
	dbPath string,
) ([]SQLiteFanoutSessionMeta, error) {
	if s.options.ListMeta == nil {
		return nil, nil
	}
	return s.options.ListMeta(dbPath)
}

func (s SQLiteFanoutSource) virtualPath() string {
	return VirtualSourcePath(s.DBPath, s.SessionID)
}
