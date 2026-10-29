package service

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

func validMemo(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model.Invalid("Memo is empty")
	}
	if len([]rune(content)) > MaxMemoLen {
		return "", model.Invalid("Memo is too long (max 20000 characters)")
	}
	return content, nil
}

// CreateMemo saves a memo; #tags are parsed from the text.
func (s *Service) CreateMemo(ctx context.Context, content string) (*model.Memo, error) {
	content, err := validMemo(content)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	m := &model.Memo{ContentMD: content, CreatedAt: now, UpdatedAt: now, Tags: markdown.ParseTags(content)}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if err := tx.CreateMemo(ctx, m); err != nil {
			return err
		}
		if err := tx.SetMemoTags(ctx, m.ID, m.Tags); err != nil {
			return err
		}
		return s.linkAttachments(ctx, tx, m.ContentMD, m.ID, 0)
	})
	return m, err
}

// UpdateMemo edits a memo's content.
func (s *Service) UpdateMemo(ctx context.Context, id int64, content string) (*model.Memo, error) {
	content, err := validMemo(content)
	if err != nil {
		return nil, err
	}
	return s.modifyMemo(ctx, id, func(m *model.Memo) error {
		m.ContentMD = content
		return nil
	})
}

// SetMemoPinned pins or unpins a memo.
func (s *Service) SetMemoPinned(ctx context.Context, id int64, pinned bool) (*model.Memo, error) {
	return s.modifyMemo(ctx, id, func(m *model.Memo) error {
		m.Pinned = pinned
		return nil
	})
}

// ToggleMemoCheckbox flips the idx-th task-list checkbox in a memo.
func (s *Service) ToggleMemoCheckbox(ctx context.Context, id int64, idx int) (*model.Memo, error) {
	return s.modifyMemo(ctx, id, func(m *model.Memo) error {
		c, err := markdown.ToggleCheckbox(m.ContentMD, idx)
		if err != nil {
			return model.Invalid("Checkbox not found")
		}
		m.ContentMD = c
		return nil
	})
}

func (s *Service) modifyMemo(ctx context.Context, id int64, fn func(*model.Memo) error) (*model.Memo, error) {
	var m *model.Memo
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if m, err = tx.GetMemo(ctx, id); err != nil {
			return err
		}
		if err := fn(m); err != nil {
			return err
		}
		m.UpdatedAt = s.now().UTC()
		m.Tags = markdown.ParseTags(m.ContentMD)
		if err := tx.UpdateMemo(ctx, m); err != nil {
			return err
		}
		if err := tx.SetMemoTags(ctx, m.ID, m.Tags); err != nil {
			return err
		}
		return s.linkAttachments(ctx, tx, m.ContentMD, m.ID, 0)
	})
	return m, err
}

// GetMemo returns a memo.
func (s *Service) GetMemo(ctx context.Context, id int64) (*model.Memo, error) {
	return s.st.GetMemo(ctx, id)
}

// DeleteMemo soft-deletes a memo.
func (s *Service) DeleteMemo(ctx context.Context, id int64) error {
	return s.st.SoftDeleteMemo(ctx, id, s.now().UTC())
}

// ListMemos returns a page of memos (newest first).
func (s *Service) ListMemos(ctx context.Context, f model.MemoFilter) ([]model.Memo, error) {
	if f.Query != "" {
		if f.Query = FTSQuery(f.Query); f.Query == "" {
			return nil, nil
		}
	}
	return s.st.ListMemos(ctx, f, false)
}

// PinnedMemos returns pinned memos.
func (s *Service) PinnedMemos(ctx context.Context) ([]model.Memo, error) {
	return s.st.ListMemos(ctx, model.MemoFilter{Limit: 100}, true)
}

// MemoCounts returns the number of memos per local day (YYYY-MM-DD) in [from, to).
func (s *Service) MemoCounts(ctx context.Context, from, to time.Time) (map[string]int, error) {
	memos, err := s.st.ListMemos(ctx, model.MemoFilter{From: &from, To: &to, Limit: 5000}, false)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, m := range memos {
		out[m.CreatedAt.In(s.loc).Format("2006-01-02")]++
	}
	return out, nil
}

// TagCounts returns tag usage counts ("memo", "task", "reminder" or "" for all).
func (s *Service) TagCounts(ctx context.Context, kind string) ([]model.TagCount, error) {
	return s.st.TagCounts(ctx, kind)
}

// Search runs full-text search over memos, tasks and reminders.
func (s *Service) Search(ctx context.Context, q string, limit int) ([]model.SearchHit, error) {
	fq := FTSQuery(q)
	if fq == "" {
		return nil, nil
	}
	return s.st.Search(ctx, fq, limit)
}

// FTSQuery converts free text into a safe FTS5 query: every word becomes a
// quoted prefix term, all terms must match.
func FTSQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_' || r == '-')
	})
	var terms []string
	for _, w := range words {
		w = strings.Trim(w, "-")
		if w == "" {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
		if len(terms) == 10 {
			break
		}
	}
	return strings.Join(terms, " ")
}
