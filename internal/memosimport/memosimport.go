// Package memosimport imports notes from a Memos (usememos.com) data folder.
// It reads a copy of memos_prod.db strictly read-only and supports the schema
// variations of recent Memos versions (pinned column or memo_organizer table,
// resource or attachment table, files in the database or in assets/).
package memosimport

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // driver

	"github.com/sovannroth/mynotes/internal/service"
)

// Options control the import.
type Options struct {
	IncludeArchived bool
	DryRun          bool
	Log             func(format string, args ...any)
}

// Report summarises an import.
type Report struct {
	Found, Imported, Skipped, Archived int
	FilesCopied                        int
	Problems                           []string
}

type memoRow struct {
	id        int64
	key       string
	content   string
	created   time.Time
	updated   time.Time
	archived  bool
	pinned    bool
	fileIDs   []int64
	fileNames []string
}

// Run imports every memo found in dir (a Memos data folder containing memos_prod.db).
func Run(ctx context.Context, svc *service.Service, dir string, opt Options) (*Report, error) {
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	dbPath := filepath.Join(dir, "memos_prod.db")
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		dbPath, dir = dir, filepath.Dir(dir) // a path to the .db file itself
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("memos database not found at %s", dbPath)
	}
	// immutable=1: read-only, no locks, never writes a -wal/-shm file.
	src, err := sql.Open("sqlite", "file:"+(&url.URL{Path: filepath.ToSlash(dbPath)}).EscapedPath()+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer src.Close()
	src.SetMaxOpenConns(1)

	r := &reader{db: src, dir: dir}
	memos, err := r.memos(ctx)
	if err != nil {
		return nil, err
	}
	rep := &Report{Found: len(memos)}
	for _, m := range memos {
		if m.archived {
			rep.Archived++
			if !opt.IncludeArchived {
				continue
			}
		}
		if opt.DryRun {
			opt.Log("would import memo %s (%s, %d file(s))", m.key, m.created.Format("2006-01-02"), len(m.fileIDs))
			rep.Imported++
			continue
		}
		in := service.ImportedMemo{Source: "memos", Key: m.key, Content: m.content, CreatedAt: m.created, UpdatedAt: m.updated, Pinned: m.pinned}
		if m.archived {
			in.Content = strings.TrimSpace(in.Content + "\n\n#archived")
		}
		for i, fid := range m.fileIDs {
			fid, name := fid, m.fileNames[i]
			in.Files = append(in.Files, service.ImportedFile{Name: name, Open: func() (io.ReadCloser, error) { return r.openFile(ctx, fid) }})
		}
		res, err := svc.ImportMemo(ctx, in)
		if err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("memo %s: %v", m.key, err))
			continue
		}
		if res.Skipped {
			rep.Skipped++
			continue
		}
		rep.Imported++
		rep.FilesCopied += len(in.Files) - len(res.FileErrors)
		for _, fe := range res.FileErrors {
			rep.Problems = append(rep.Problems, fmt.Sprintf("memo %s file %s", m.key, fe))
		}
		opt.Log("imported memo %s -> #%d (%s)", m.key, res.MemoID, m.created.Format("2006-01-02 15:04"))
	}
	return rep, nil
}

type reader struct {
	db       *sql.DB
	dir      string
	fileTbl  string
	fileCols map[string]bool
}

func (r *reader) columns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		cols[n] = true
	}
	return cols, rows.Err()
}

func pick(cols map[string]bool, name, fallback string) string {
	if cols[name] {
		return name
	}
	return fallback
}

func (r *reader) memos(ctx context.Context) ([]*memoRow, error) {
	cols, err := r.columns(ctx, "memo")
	if err != nil {
		return nil, err
	}
	if !cols["content"] || !cols["created_ts"] {
		return nil, errors.New("this does not look like a Memos database (table memo not found)")
	}
	q := fmt.Sprintf(`SELECT id, %s, content, created_ts, %s, %s, %s FROM memo`,
		pick(cols, "uid", "''"), pick(cols, "updated_ts", "created_ts"), pick(cols, "row_status", "'NORMAL'"), pick(cols, "pinned", "0"))
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*memoRow{}
	var list []*memoRow
	for rows.Next() {
		m := &memoRow{}
		var uid, status string
		var created, updated int64
		var pinned int
		if err := rows.Scan(&m.id, &uid, &m.content, &created, &updated, &status, &pinned); err != nil {
			rows.Close()
			return nil, err
		}
		m.key = uid
		if m.key == "" {
			m.key = "id:" + strconv.FormatInt(m.id, 10)
		}
		m.created, m.updated = unix(created), unix(updated)
		m.archived = strings.EqualFold(status, "ARCHIVED")
		m.pinned = pinned != 0
		byID[m.id] = m
		list = append(list, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Older versions keep pinned state in memo_organizer.
	if !cols["pinned"] {
		if oc, _ := r.columns(ctx, "memo_organizer"); oc["pinned"] {
			prow, err := r.db.QueryContext(ctx, `SELECT memo_id FROM memo_organizer WHERE pinned = 1`)
			if err == nil {
				for prow.Next() {
					var id int64
					if prow.Scan(&id) == nil && byID[id] != nil {
						byID[id].pinned = true
					}
				}
				prow.Close()
			}
		}
	}

	// Attachments: table "attachment" (newer) or "resource" (older).
	for _, t := range []string{"attachment", "resource"} {
		c, err := r.columns(ctx, t)
		if err == nil && c["memo_id"] && c["filename"] {
			r.fileTbl, r.fileCols = t, c
			break
		}
	}
	if r.fileTbl != "" {
		frows, err := r.db.QueryContext(ctx, `SELECT id, memo_id, filename FROM `+r.fileTbl+` WHERE memo_id IS NOT NULL ORDER BY id`)
		if err != nil {
			return nil, err
		}
		for frows.Next() {
			var id, memoID int64
			var name string
			if err := frows.Scan(&id, &memoID, &name); err != nil {
				frows.Close()
				return nil, err
			}
			if m := byID[memoID]; m != nil {
				m.fileIDs = append(m.fileIDs, id)
				m.fileNames = append(m.fileNames, name)
			}
		}
		frows.Close()
	}
	sort.Slice(list, func(i, j int) bool { return list[i].created.Before(list[j].created) })
	return list, nil
}

func unix(ts int64) time.Time {
	if ts > 1e12 { // milliseconds
		return time.UnixMilli(ts).UTC()
	}
	return time.Unix(ts, 0).UTC()
}

// openFile returns a file's bytes from the database blob or the assets folder.
func (r *reader) openFile(ctx context.Context, id int64) (io.ReadCloser, error) {
	ref := ""
	for _, c := range []string{"reference", "internal_path"} {
		if r.fileCols[c] {
			_ = r.db.QueryRowContext(ctx, `SELECT COALESCE(`+c+`, '') FROM `+r.fileTbl+` WHERE id = ?`, id).Scan(&ref)
			if ref != "" {
				break
			}
		}
	}
	storage := ""
	if r.fileCols["storage_type"] {
		_ = r.db.QueryRowContext(ctx, `SELECT COALESCE(storage_type, '') FROM `+r.fileTbl+` WHERE id = ?`, id).Scan(&storage)
	}
	if r.fileCols["blob"] {
		var blob []byte
		if err := r.db.QueryRowContext(ctx, `SELECT blob FROM `+r.fileTbl+` WHERE id = ?`, id).Scan(&blob); err == nil && len(blob) > 0 {
			return io.NopCloser(bytes.NewReader(blob)), nil
		}
	}
	if strings.EqualFold(storage, "S3") || strings.EqualFold(storage, "EXTERNAL") || strings.HasPrefix(ref, "http") {
		return nil, errors.New("stored outside Memos (S3/external link), not copied")
	}
	if ref == "" {
		return nil, errors.New("no file data found")
	}
	// The reference is relative to the Memos data folder ("assets/...") or an
	// absolute path inside the Memos container (/var/opt/memos/assets/...).
	ref = filepath.ToSlash(ref)
	candidates := []string{}
	if i := strings.Index(ref, "assets/"); i >= 0 {
		candidates = append(candidates, filepath.Join(r.dir, filepath.FromSlash(ref[i:])))
	}
	if !strings.HasPrefix(ref, "/") {
		candidates = append(candidates, filepath.Join(r.dir, filepath.FromSlash(ref)))
	}
	candidates = append(candidates, filepath.Join(r.dir, "assets", filepath.Base(ref)))
	for _, p := range candidates {
		clean := filepath.Clean(p)
		if rel, err := filepath.Rel(r.dir, clean); err != nil || strings.HasPrefix(rel, "..") {
			continue // never read outside the data folder
		}
		if f, err := os.Open(clean); err == nil { //nolint:gosec // confined to the data folder above
			return f, nil
		}
	}
	return nil, fmt.Errorf("file not found in %s (looked for %s)", r.dir, ref)
}
