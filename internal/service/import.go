package service

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

// ImportedFile is a file to attach to an imported memo.
type ImportedFile struct {
	Name string
	Open func() (io.ReadCloser, error)
}

// ImportedMemo is a memo coming from another app.
type ImportedMemo struct {
	Source    string // e.g. "memos"
	Key       string // stable id in the source app
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
	Pinned    bool
	Files     []ImportedFile
}

// ImportResult describes what happened to one imported memo.
type ImportResult struct {
	MemoID     int64
	Skipped    bool     // already imported earlier
	FileErrors []string // files that could not be copied
}

// ImportMemo creates a memo keeping its original timestamps. Attached files are
// stored like normal uploads and linked at the end of the memo. Importing the
// same Source/Key twice is a no-op.
func (s *Service) ImportMemo(ctx context.Context, in ImportedMemo) (ImportResult, error) {
	var res ImportResult
	id, err := s.st.ImportedID(ctx, in.Source, in.Key)
	if err != nil {
		return res, err
	}
	if id > 0 {
		return ImportResult{MemoID: id, Skipped: true}, nil
	}
	content := strings.TrimSpace(in.Content)
	if content == "" && len(in.Files) == 0 {
		return ImportResult{Skipped: true}, nil
	}
	if len(content) > MaxNoteLen {
		return res, model.Invalid("memo is longer than 200,000 characters")
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = s.now()
	}
	if in.UpdatedAt.Before(in.CreatedAt) {
		in.UpdatedAt = in.CreatedAt
	}
	m := &model.Memo{ContentMD: content, Pinned: in.Pinned, CreatedAt: in.CreatedAt.UTC(), UpdatedAt: in.UpdatedAt.UTC(), Tags: markdown.ParseTags(content)}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if err := tx.CreateMemo(ctx, m); err != nil {
			return err
		}
		return tx.SetMemoTags(ctx, m.ID, m.Tags)
	})
	if err != nil {
		return res, err
	}
	res.MemoID = m.ID

	// Copy files (outside a transaction: SaveUpload manages its own).
	var links []string
	for _, f := range in.Files {
		rc, err := f.Open()
		if err != nil {
			res.FileErrors = append(res.FileErrors, f.Name+": "+err.Error())
			continue
		}
		a, err := s.SaveUpload(ctx, rc, f.Name, UploadOwner{MemoID: m.ID})
		_ = rc.Close()
		if err != nil {
			res.FileErrors = append(res.FileErrors, f.Name+": "+err.Error())
			continue
		}
		links = append(links, AttachmentMarkdown(a))
	}
	if len(links) > 0 {
		m.ContentMD = strings.TrimSpace(m.ContentMD + "\n\n" + strings.Join(links, "\n"))
		err = s.st.InTx(ctx, func(tx store.Store) error { return tx.UpdateMemo(ctx, m) }) // keeps the original updated_at
		if err != nil {
			return res, err
		}
	}
	return res, s.st.RecordImport(ctx, in.Source, in.Key, "memo", m.ID, s.now().UTC())
}
