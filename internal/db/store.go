package db

import (
    "context"
    "database/sql"
    "errors"
    "os"
    "path/filepath"
    "time"

    _ "modernc.org/sqlite"
)

type Store struct { db *sql.DB }

func workspacePath() (string, error) {
    h, err := os.UserHomeDir(); if err != nil { return "", err }
    d := filepath.Join(h, ".compair")
    if err := os.MkdirAll(d, 0o700); err != nil { return "", err }
    return filepath.Join(d, "workspace.db"), nil
}

func Open() (*Store, error) {
    p, err := workspacePath(); if err != nil { return nil, err }
    db, err := sql.Open("sqlite", p)
    if err != nil { return nil, err }
    s := &Store{db: db}
    if err := s.ensureSchema(); err != nil { _ = db.Close(); return nil, err }
    return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ensureSchema() error {
    stmts := []string{
        `PRAGMA journal_mode=WAL;`,
        `CREATE TABLE IF NOT EXISTS tracked_items (
            id INTEGER PRIMARY KEY,
            path TEXT NOT NULL,
            kind TEXT NOT NULL CHECK(kind IN ('file','dir','repo')),
            group_id TEXT NOT NULL,
            document_id TEXT,
            repo_root TEXT,
            last_synced_commit TEXT,
            file_sig TEXT,
            content_hash TEXT,
            size INTEGER,
            mtime INTEGER,
            last_synced_at INTEGER,
            last_seen_at INTEGER,
            is_published INTEGER DEFAULT 0,
            UNIQUE(path, group_id)
        );`,
        `CREATE TABLE IF NOT EXISTS tracked_aliases (
            item_id INTEGER NOT NULL REFERENCES tracked_items(id) ON DELETE CASCADE,
            path TEXT NOT NULL,
            link_type TEXT CHECK(link_type IN ('hardlink','symlink')),
            link_target TEXT,
            is_primary INTEGER DEFAULT 0,
            UNIQUE(item_id, path)
        );`,
        `CREATE INDEX IF NOT EXISTS idx_items_sig_group ON tracked_items(file_sig, group_id);`,
        `CREATE INDEX IF NOT EXISTS idx_items_doc ON tracked_items(document_id);`,
        `CREATE INDEX IF NOT EXISTS idx_alias_path ON tracked_aliases(path);`,
    }
    for _, st := range stmts {
        if _, err := s.db.Exec(st); err != nil { return err }
    }
    // Attempt to add missing columns on existing DBs (ignore errors if exists)
    _, _ = s.db.Exec(`ALTER TABLE tracked_items ADD COLUMN is_published INTEGER DEFAULT 0`)
    return nil
}

type TrackedItem struct {
    ID int64
    Path string
    Kind string
    GroupID string
    DocumentID string
    RepoRoot string
    LastSyncedCommit string
    FileSig string
    ContentHash string
    Size int64
    MTime int64
    LastSyncedAt int64
    LastSeenAt int64
    Published int64
}

func (s *Store) UpsertItem(ctx context.Context, ti *TrackedItem) error {
    if ti.Path == "" || ti.GroupID == "" || ti.Kind == "" { return errors.New("missing required fields") }
    // Insert or update selected fields
    _, err := s.db.ExecContext(ctx, `INSERT INTO tracked_items
        (path, kind, group_id, document_id, repo_root, last_synced_commit, file_sig, content_hash, size, mtime, last_synced_at, last_seen_at, is_published)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(path, group_id) DO UPDATE SET
          kind=excluded.kind,
          document_id=COALESCE(excluded.document_id, tracked_items.document_id),
          repo_root=COALESCE(excluded.repo_root, tracked_items.repo_root),
          last_synced_commit=COALESCE(excluded.last_synced_commit, tracked_items.last_synced_commit),
          file_sig=COALESCE(excluded.file_sig, tracked_items.file_sig),
          content_hash=COALESCE(excluded.content_hash, tracked_items.content_hash),
          size=CASE WHEN excluded.size>0 THEN excluded.size ELSE tracked_items.size END,
          mtime=CASE WHEN excluded.mtime>0 THEN excluded.mtime ELSE tracked_items.mtime END,
          last_seen_at=excluded.last_seen_at,
          is_published=CASE WHEN excluded.is_published IN (0,1) THEN excluded.is_published ELSE tracked_items.is_published END`,
        ti.Path, ti.Kind, ti.GroupID, ti.DocumentID, ti.RepoRoot, ti.LastSyncedCommit, ti.FileSig, ti.ContentHash, ti.Size, ti.MTime, ti.LastSyncedAt, nowEpoch(), ti.Published)
    return err
}

func (s *Store) DeleteByPathGroup(ctx context.Context, path, group string) (int64, error) {
    res, err := s.db.ExecContext(ctx, `DELETE FROM tracked_items WHERE path=? AND group_id=?`, path, group)
    if err != nil { return 0, err }
    n, _ := res.RowsAffected(); return n, nil
}

func (s *Store) ListUnderPrefix(ctx context.Context, prefix, group string) ([]TrackedItem, error) {
    rows, err := s.db.QueryContext(ctx, `SELECT id, path, kind, group_id, document_id, repo_root, last_synced_commit, file_sig, content_hash, size, mtime, last_synced_at, last_seen_at, is_published FROM tracked_items WHERE group_id=? AND path LIKE ? ORDER BY path`, group, prefix+"%")
    if err != nil { return nil, err }
    defer rows.Close()
    var out []TrackedItem
    for rows.Next() {
        var ti TrackedItem
        if err := rows.Scan(&ti.ID, &ti.Path, &ti.Kind, &ti.GroupID, &ti.DocumentID, &ti.RepoRoot, &ti.LastSyncedCommit, &ti.FileSig, &ti.ContentHash, &ti.Size, &ti.MTime, &ti.LastSyncedAt, &ti.LastSeenAt, &ti.Published); err != nil { return nil, err }
        out = append(out, ti)
    }
    return out, nil
}

func (s *Store) ListByGroup(ctx context.Context, group string) ([]TrackedItem, error) {
    rows, err := s.db.QueryContext(ctx, `SELECT id, path, kind, group_id, document_id, repo_root, last_synced_commit, file_sig, content_hash, size, mtime, last_synced_at, last_seen_at, is_published FROM tracked_items WHERE group_id=? ORDER BY path`, group)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []TrackedItem
    for rows.Next() {
        var ti TrackedItem
        if err := rows.Scan(&ti.ID, &ti.Path, &ti.Kind, &ti.GroupID, &ti.DocumentID, &ti.RepoRoot, &ti.LastSyncedCommit, &ti.FileSig, &ti.ContentHash, &ti.Size, &ti.MTime, &ti.LastSyncedAt, &ti.LastSeenAt, &ti.Published); err != nil { return nil, err }
        out = append(out, ti)
    }
    return out, nil
}

func (s *Store) FindByPathGroup(ctx context.Context, path, group string) (*TrackedItem, error) {
    row := s.db.QueryRowContext(ctx, `SELECT id, path, kind, group_id, document_id, repo_root, last_synced_commit, file_sig, content_hash, size, mtime, last_synced_at, last_seen_at, is_published FROM tracked_items WHERE group_id=? AND path=?`, group, path)
    var ti TrackedItem
    if err := row.Scan(&ti.ID, &ti.Path, &ti.Kind, &ti.GroupID, &ti.DocumentID, &ti.RepoRoot, &ti.LastSyncedCommit, &ti.FileSig, &ti.ContentHash, &ti.Size, &ti.MTime, &ti.LastSyncedAt, &ti.LastSeenAt, &ti.Published); err != nil { return nil, err }
    return &ti, nil
}

func (s *Store) DistinctGroups(ctx context.Context) ([]string, error) {
    rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT group_id FROM tracked_items ORDER BY group_id`)
    if err != nil { return nil, err }
    defer rows.Close()
    var ids []string
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil { return nil, err }
        ids = append(ids, id)
    }
    return ids, nil
}

// ListRepoRoots returns distinct repo roots tracked for a group.
func (s *Store) ListRepoRoots(ctx context.Context, group string) ([]string, error) {
    // Prefer explicit repo_root; also include rows where kind='repo'.
    rows, err := s.db.QueryContext(ctx, `
        SELECT DISTINCT repo_root FROM tracked_items WHERE group_id=? AND repo_root<>''
        UNION
        SELECT DISTINCT path AS repo_root FROM tracked_items WHERE group_id=? AND kind='repo'` , group, group)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []string
    for rows.Next() {
        var root sql.NullString
        if err := rows.Scan(&root); err != nil { return nil, err }
        if root.Valid && root.String != "" { out = append(out, root.String) }
    }
    return out, nil
}

func nowEpoch() int64 { return time.Now().Unix() }
