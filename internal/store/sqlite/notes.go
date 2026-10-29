package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

var noteTagLink = tagLink{"note_tags", "note_id"}

// ---- folders ----

const folderCols = `f.id, f.parent_id, f.name, f.created_at, f.updated_at, f.deleted_at`

func scanFolder(sc interface{ Scan(...any) error }) (model.Folder, error) {
	var f model.Folder
	err := sc.Scan(&f.ID, ip(&f.ParentID), &f.Name, ts(&f.CreatedAt), ts(&f.UpdatedAt), tsp(&f.DeletedAt))
	return f, err
}

// CreateFolder implements store.Notes.
func (d *DB) CreateFolder(ctx context.Context, f *model.Folder) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO folders(parent_id, name, created_at, updated_at) VALUES (?,?,?,?)`,
		nullInt(f.ParentID), f.Name, ft(f.CreatedAt), ft(f.UpdatedAt))
	if err != nil {
		return err
	}
	f.ID, err = res.LastInsertId()
	return err
}

// UpdateFolder implements store.Notes.
func (d *DB) UpdateFolder(ctx context.Context, f *model.Folder) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE folders SET parent_id=?, name=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		nullInt(f.ParentID), f.Name, ft(f.UpdatedAt), f.ID))
}

// GetFolder implements store.Notes.
func (d *DB) GetFolder(ctx context.Context, id int64) (*model.Folder, error) {
	f, err := scanFolder(d.q.QueryRowContext(ctx, `SELECT `+folderCols+` FROM folders f WHERE f.id = ? AND f.deleted_at IS NULL`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

// ListFolders implements store.Notes.
func (d *DB) ListFolders(ctx context.Context) ([]model.Folder, error) {
	rows, err := d.q.QueryContext(ctx, `SELECT `+folderCols+` FROM folders f WHERE f.deleted_at IS NULL ORDER BY lower(f.name), f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SoftDeleteFolder implements store.Notes.
func (d *DB) SoftDeleteFolder(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE folders SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, ft(at), ft(at), id))
}

// ---- notes ----

const noteCols = `n.id, n.folder_id, n.title, n.content_md, n.pinned, n.created_at, n.updated_at, n.deleted_at`

func scanNote(sc interface{ Scan(...any) error }) (model.Note, error) {
	var n model.Note
	err := sc.Scan(&n.ID, ip(&n.FolderID), &n.Title, &n.ContentMD, &n.Pinned, ts(&n.CreatedAt), ts(&n.UpdatedAt), tsp(&n.DeletedAt))
	return n, err
}

// CreateNote implements store.Notes.
func (d *DB) CreateNote(ctx context.Context, n *model.Note) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO notes(folder_id, title, content_md, pinned, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		nullInt(n.FolderID), n.Title, n.ContentMD, b2i(n.Pinned), ft(n.CreatedAt), ft(n.UpdatedAt))
	if err != nil {
		return err
	}
	n.ID, err = res.LastInsertId()
	return err
}

// UpdateNote implements store.Notes.
func (d *DB) UpdateNote(ctx context.Context, n *model.Note) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE notes SET folder_id=?, title=?, content_md=?, pinned=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		nullInt(n.FolderID), n.Title, n.ContentMD, b2i(n.Pinned), ft(n.UpdatedAt), n.ID))
}

// GetNote implements store.Notes.
func (d *DB) GetNote(ctx context.Context, id int64) (*model.Note, error) {
	n, err := scanNote(d.q.QueryRowContext(ctx, `SELECT `+noteCols+` FROM notes n WHERE n.id = ? AND n.deleted_at IS NULL`, id))
	if err != nil {
		return nil, notFound(err)
	}
	tags, err := d.loadTags(ctx, noteTagLink, []int64{id})
	if err != nil {
		return nil, err
	}
	n.Tags = tags[id]
	return &n, nil
}

// ListNotes implements store.Notes.
func (d *DB) ListNotes(ctx context.Context, folderID int64, all bool) ([]model.Note, error) {
	q := `SELECT ` + noteCols + ` FROM notes n WHERE n.deleted_at IS NULL`
	var args []any
	switch {
	case all:
	case folderID == 0:
		q += ` AND n.folder_id IS NULL`
	default:
		q += ` AND n.folder_id = ?`
		args = append(args, folderID)
	}
	q += ` ORDER BY n.pinned DESC, n.updated_at DESC, n.id DESC LIMIT 2000`
	rows, err := d.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []model.Note
	var ids []int64
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, n)
		ids = append(ids, n.ID)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := d.loadTags(ctx, noteTagLink, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}

// SoftDeleteNote implements store.Notes.
func (d *DB) SoftDeleteNote(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE notes SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, ft(at), id))
}

// SetNoteTags implements store.Notes.
func (d *DB) SetNoteTags(ctx context.Context, id int64, tags []string) error {
	return d.setTags(ctx, noteTagLink, id, tags)
}

// ---- attachments ----

const attCols = `a.id, a.folder_id, a.memo_id, a.note_id, a.filename, a.mime, a.size, a.sha256, a.storage_key, a.created_at, a.updated_at, a.deleted_at`

func scanAtt(sc interface{ Scan(...any) error }) (model.Attachment, error) {
	var a model.Attachment
	err := sc.Scan(&a.ID, ip(&a.FolderID), ip(&a.MemoID), ip(&a.NoteID), &a.Filename, &a.Mime, &a.Size, &a.SHA256, &a.StorageKey,
		ts(&a.CreatedAt), ts(&a.UpdatedAt), tsp(&a.DeletedAt))
	return a, err
}

// CreateAttachment implements store.Attachments.
func (d *DB) CreateAttachment(ctx context.Context, a *model.Attachment) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO attachments(folder_id, memo_id, note_id, filename, mime, size, sha256, storage_key, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		nullInt(a.FolderID), nullInt(a.MemoID), nullInt(a.NoteID), a.Filename, a.Mime, a.Size, a.SHA256, a.StorageKey, ft(a.CreatedAt), ft(a.UpdatedAt))
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

// UpdateAttachment implements store.Attachments.
func (d *DB) UpdateAttachment(ctx context.Context, a *model.Attachment) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE attachments SET folder_id=?, memo_id=?, note_id=?, filename=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		nullInt(a.FolderID), nullInt(a.MemoID), nullInt(a.NoteID), a.Filename, ft(a.UpdatedAt), a.ID))
}

// GetAttachment implements store.Attachments.
func (d *DB) GetAttachment(ctx context.Context, id int64) (*model.Attachment, error) {
	a, err := scanAtt(d.q.QueryRowContext(ctx, `SELECT `+attCols+` FROM attachments a WHERE a.id = ? AND a.deleted_at IS NULL`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

// ListAttachments implements store.Attachments.
func (d *DB) ListAttachments(ctx context.Context, f model.AttachmentFilter) ([]model.Attachment, error) {
	q := `SELECT ` + attCols + ` FROM attachments a WHERE a.deleted_at IS NULL`
	var args []any
	switch {
	case f.InFolder && f.FolderID == 0:
		q += ` AND a.folder_id IS NULL AND a.memo_id IS NULL AND a.note_id IS NULL`
	case f.InFolder:
		q += ` AND a.folder_id = ?`
		args = append(args, f.FolderID)
	case f.MemoID > 0:
		q += ` AND a.memo_id = ?`
		args = append(args, f.MemoID)
	case f.NoteID > 0:
		q += ` AND a.note_id = ?`
		args = append(args, f.NoteID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	args = append(args, limit)
	rows, err := d.q.QueryContext(ctx, q+` ORDER BY lower(a.filename), a.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Attachment
	for rows.Next() {
		a, err := scanAtt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SoftDeleteAttachment implements store.Attachments.
func (d *DB) SoftDeleteAttachment(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE attachments SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, ft(at), ft(at), id))
}

// ---- imports ----

// ImportedID implements store.Imports.
func (d *DB) ImportedID(ctx context.Context, source, key string) (int64, error) {
	var id int64
	err := d.q.QueryRowContext(ctx, `SELECT target_id FROM imports WHERE source = ? AND source_key = ?`, source, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// RecordImport implements store.Imports.
func (d *DB) RecordImport(ctx context.Context, source, key, kind string, id int64, at time.Time) error {
	_, err := d.q.ExecContext(ctx, `INSERT INTO imports(source, source_key, target_kind, target_id, imported_at) VALUES (?,?,?,?,?)
		ON CONFLICT(source, source_key) DO UPDATE SET target_kind = excluded.target_kind, target_id = excluded.target_id, imported_at = excluded.imported_at`,
		source, key, kind, id, ft(at))
	return err
}
