package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

// Limits for folders and notes.
const (
	MaxNameLen    = 120
	MaxNoteLen    = 200000
	maxFolderDeep = 32
)

func validName(name, what string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", model.Invalid(what + " name is required")
	case len([]rune(name)) > MaxNameLen:
		return "", model.Invalid(what + " name is too long")
	case strings.ContainsAny(name, "/\\\x00") || name == "." || name == "..":
		return "", model.Invalid(what + ` name cannot contain / or \`)
	}
	return name, nil
}

func sameParent(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func normParent(id *int64) *int64 {
	if id == nil || *id <= 0 {
		return nil
	}
	return id
}

// checkSiblingName rejects a folder name already used by a sibling (case-insensitive).
func checkSiblingName(ctx context.Context, tx store.Store, parent *int64, name string, selfID int64) error {
	all, err := tx.ListFolders(ctx)
	if err != nil {
		return err
	}
	for _, f := range all {
		if f.ID != selfID && sameParent(f.ParentID, parent) && strings.EqualFold(f.Name, name) {
			return model.Invalid("A folder named “" + name + "” already exists here")
		}
	}
	return nil
}

func (s *Service) requireFolder(ctx context.Context, tx store.Store, id *int64) error {
	if id == nil {
		return nil
	}
	if _, err := tx.GetFolder(ctx, *id); err != nil {
		return model.Invalid("Folder not found")
	}
	return nil
}

// CreateFolder creates a folder (parentID nil = top level).
func (s *Service) CreateFolder(ctx context.Context, name string, parentID *int64) (*model.Folder, error) {
	name, err := validName(name, "Folder")
	if err != nil {
		return nil, err
	}
	parentID = normParent(parentID)
	now := s.now().UTC()
	f := &model.Folder{ParentID: parentID, Name: name, CreatedAt: now, UpdatedAt: now}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if err := s.requireFolder(ctx, tx, parentID); err != nil {
			return err
		}
		if parentID != nil {
			path, err := folderPath(ctx, tx, *parentID)
			if err != nil {
				return err
			}
			if len(path) >= maxFolderDeep {
				return model.Invalid("Folders are nested too deeply")
			}
		}
		if err := checkSiblingName(ctx, tx, parentID, name, 0); err != nil {
			return err
		}
		return tx.CreateFolder(ctx, f)
	})
	return f, err
}

// RenameFolder renames a folder.
func (s *Service) RenameFolder(ctx context.Context, id int64, name string) (*model.Folder, error) {
	name, err := validName(name, "Folder")
	if err != nil {
		return nil, err
	}
	var f *model.Folder
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if f, err = tx.GetFolder(ctx, id); err != nil {
			return err
		}
		if err := checkSiblingName(ctx, tx, f.ParentID, name, f.ID); err != nil {
			return err
		}
		f.Name, f.UpdatedAt = name, s.now().UTC()
		return tx.UpdateFolder(ctx, f)
	})
	return f, err
}

// MoveFolder moves a folder under a new parent (nil = top level). Moving a
// folder into itself or one of its descendants is rejected.
func (s *Service) MoveFolder(ctx context.Context, id int64, parentID *int64) (*model.Folder, error) {
	parentID = normParent(parentID)
	var f *model.Folder
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if f, err = tx.GetFolder(ctx, id); err != nil {
			return err
		}
		if parentID != nil {
			path, err := folderPath(ctx, tx, *parentID)
			if err != nil {
				return model.Invalid("Target folder not found")
			}
			for _, p := range path {
				if p.ID == id {
					return model.Invalid("A folder cannot be moved into itself")
				}
			}
		}
		if err := checkSiblingName(ctx, tx, parentID, f.Name, f.ID); err != nil {
			return err
		}
		f.ParentID, f.UpdatedAt = parentID, s.now().UTC()
		return tx.UpdateFolder(ctx, f)
	})
	return f, err
}

// DeleteFolder soft-deletes a folder with all its subfolders, notes and files.
func (s *Service) DeleteFolder(ctx context.Context, id int64) error {
	now := s.now().UTC()
	return s.st.InTx(ctx, func(tx store.Store) error {
		if _, err := tx.GetFolder(ctx, id); err != nil {
			return err
		}
		all, err := tx.ListFolders(ctx)
		if err != nil {
			return err
		}
		children := map[int64][]int64{}
		for _, f := range all {
			if f.ParentID != nil {
				children[*f.ParentID] = append(children[*f.ParentID], f.ID)
			}
		}
		var del func(fid int64) error
		del = func(fid int64) error {
			for _, c := range children[fid] {
				if err := del(c); err != nil {
					return err
				}
			}
			notes, err := tx.ListNotes(ctx, fid, false)
			if err != nil {
				return err
			}
			for _, n := range notes {
				if err := s.deleteNoteTx(ctx, tx, n.ID, now); err != nil {
					return err
				}
			}
			files, err := tx.ListAttachments(ctx, model.AttachmentFilter{InFolder: true, FolderID: fid})
			if err != nil {
				return err
			}
			for _, a := range files {
				if err := tx.SoftDeleteAttachment(ctx, a.ID, now); err != nil {
					return err
				}
			}
			return tx.SoftDeleteFolder(ctx, fid, now)
		}
		return del(id)
	})
}

// GetFolder returns a folder.
func (s *Service) GetFolder(ctx context.Context, id int64) (*model.Folder, error) {
	return s.st.GetFolder(ctx, id)
}

// FolderPath returns the folders from the top level down to id (inclusive).
func (s *Service) FolderPath(ctx context.Context, id int64) ([]model.Folder, error) {
	return folderPath(ctx, s.st, id)
}

func folderPath(ctx context.Context, st store.Store, id int64) ([]model.Folder, error) {
	var path []model.Folder
	cur := &id
	for i := 0; cur != nil && i <= maxFolderDeep+1; i++ {
		f, err := st.GetFolder(ctx, *cur)
		if err != nil {
			return nil, err
		}
		path = append([]model.Folder{*f}, path...)
		cur = f.ParentID
	}
	return path, nil
}

// FolderNode is a folder with its children, for tree views.
type FolderNode struct {
	Folder   model.Folder  `json:"folder"`
	Path     string        `json:"path"`
	Depth    int           `json:"depth"`
	Children []*FolderNode `json:"children,omitempty"`
}

// FolderTree returns the folder hierarchy and a flat depth-first list (for pickers).
func (s *Service) FolderTree(ctx context.Context) ([]*FolderNode, []*FolderNode, error) {
	all, err := s.st.ListFolders(ctx)
	if err != nil {
		return nil, nil, err
	}
	nodes := map[int64]*FolderNode{}
	for _, f := range all {
		nodes[f.ID] = &FolderNode{Folder: f}
	}
	var roots []*FolderNode
	for _, f := range all {
		n := nodes[f.ID]
		if f.ParentID != nil {
			if p, ok := nodes[*f.ParentID]; ok {
				p.Children = append(p.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	var flat []*FolderNode
	var walk func(ns []*FolderNode, prefix string, depth int)
	walk = func(ns []*FolderNode, prefix string, depth int) {
		sort.Slice(ns, func(i, j int) bool { return strings.ToLower(ns[i].Folder.Name) < strings.ToLower(ns[j].Folder.Name) })
		for _, n := range ns {
			n.Path, n.Depth = prefix+n.Folder.Name, depth
			flat = append(flat, n)
			walk(n.Children, n.Path+" / ", depth+1)
		}
	}
	walk(roots, "", 0)
	return roots, flat, nil
}

// FolderContents is everything directly inside a folder.
type FolderContents struct {
	Folder  *model.Folder      `json:"folder"` // nil = top level
	Path    []model.Folder     `json:"path"`
	Folders []model.Folder     `json:"folders"`
	Notes   []model.Note       `json:"notes"`
	Files   []model.Attachment `json:"files"`
}

// Contents lists a folder (id 0 = top level).
func (s *Service) Contents(ctx context.Context, id int64) (*FolderContents, error) {
	c := &FolderContents{}
	if id > 0 {
		f, err := s.st.GetFolder(ctx, id)
		if err != nil {
			return nil, err
		}
		c.Folder = f
		if c.Path, err = folderPath(ctx, s.st, id); err != nil {
			return nil, err
		}
	}
	all, err := s.st.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range all {
		if (id == 0 && f.ParentID == nil) || (f.ParentID != nil && *f.ParentID == id) {
			c.Folders = append(c.Folders, f)
		}
	}
	if c.Notes, err = s.st.ListNotes(ctx, id, false); err != nil {
		return nil, err
	}
	if c.Files, err = s.st.ListAttachments(ctx, model.AttachmentFilter{InFolder: true, FolderID: id}); err != nil {
		return nil, err
	}
	return c, nil
}

// ---- notes ----

func validNote(title, content string) (string, string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	if len([]rune(title)) > MaxTitleLen {
		return "", "", model.Invalid("Title is too long")
	}
	if len(content) > MaxNoteLen {
		return "", "", model.Invalid("Note is too long (max 200,000 characters)")
	}
	return title, content, nil
}

// CreateNote creates a note in a folder (nil = top level).
func (s *Service) CreateNote(ctx context.Context, folderID *int64, title, content string) (*model.Note, error) {
	title, content, err := validNote(title, content)
	if err != nil {
		return nil, err
	}
	folderID = normParent(folderID)
	now := s.now().UTC()
	n := &model.Note{FolderID: folderID, Title: title, ContentMD: content, CreatedAt: now, UpdatedAt: now, Tags: markdown.ParseTags(content)}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if err := s.requireFolder(ctx, tx, folderID); err != nil {
			return err
		}
		if err := tx.CreateNote(ctx, n); err != nil {
			return err
		}
		if err := tx.SetNoteTags(ctx, n.ID, n.Tags); err != nil {
			return err
		}
		return s.linkAttachments(ctx, tx, content, 0, n.ID)
	})
	return n, err
}

// UpdateNote saves a note's title and content.
func (s *Service) UpdateNote(ctx context.Context, id int64, title, content string) (*model.Note, error) {
	title, content, err := validNote(title, content)
	if err != nil {
		return nil, err
	}
	return s.modifyNote(ctx, id, func(tx store.Store, n *model.Note) error {
		n.Title, n.ContentMD = title, content
		return nil
	})
}

// MoveNote moves a note to another folder (nil = top level).
func (s *Service) MoveNote(ctx context.Context, id int64, folderID *int64) (*model.Note, error) {
	folderID = normParent(folderID)
	return s.modifyNote(ctx, id, func(tx store.Store, n *model.Note) error {
		n.FolderID = folderID
		return s.requireFolder(ctx, tx, folderID)
	})
}

// SetNotePinned pins or unpins a note.
func (s *Service) SetNotePinned(ctx context.Context, id int64, pinned bool) (*model.Note, error) {
	return s.modifyNote(ctx, id, func(tx store.Store, n *model.Note) error {
		n.Pinned = pinned
		return nil
	})
}

// ToggleNoteCheckbox flips the idx-th task-list checkbox in a note.
func (s *Service) ToggleNoteCheckbox(ctx context.Context, id int64, idx int) (*model.Note, error) {
	return s.modifyNote(ctx, id, func(tx store.Store, n *model.Note) error {
		c, err := markdown.ToggleCheckbox(n.ContentMD, idx)
		if err != nil {
			return model.Invalid("Checkbox not found")
		}
		n.ContentMD = c
		return nil
	})
}

func (s *Service) modifyNote(ctx context.Context, id int64, fn func(store.Store, *model.Note) error) (*model.Note, error) {
	var n *model.Note
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if n, err = tx.GetNote(ctx, id); err != nil {
			return err
		}
		if err := fn(tx, n); err != nil {
			return err
		}
		n.UpdatedAt = s.now().UTC()
		n.Tags = markdown.ParseTags(n.ContentMD)
		if err := tx.UpdateNote(ctx, n); err != nil {
			return err
		}
		if err := tx.SetNoteTags(ctx, n.ID, n.Tags); err != nil {
			return err
		}
		return s.linkAttachments(ctx, tx, n.ContentMD, 0, n.ID)
	})
	return n, err
}

// GetNote returns a note.
func (s *Service) GetNote(ctx context.Context, id int64) (*model.Note, error) {
	return s.st.GetNote(ctx, id)
}

// ListAllNotes returns every note (newest first).
func (s *Service) ListAllNotes(ctx context.Context) ([]model.Note, error) {
	return s.st.ListNotes(ctx, 0, true)
}

// DeleteNote soft-deletes a note and its attachments.
func (s *Service) DeleteNote(ctx context.Context, id int64) error {
	now := s.now().UTC()
	return s.st.InTx(ctx, func(tx store.Store) error { return s.deleteNoteTx(ctx, tx, id, now) })
}

func (s *Service) deleteNoteTx(ctx context.Context, tx store.Store, id int64, now time.Time) error {
	if err := tx.SoftDeleteNote(ctx, id, now); err != nil {
		return err
	}
	files, err := tx.ListAttachments(ctx, model.AttachmentFilter{NoteID: id})
	if err != nil {
		return err
	}
	for _, a := range files {
		if err := tx.SoftDeleteAttachment(ctx, a.ID, now); err != nil {
			return err
		}
	}
	return nil
}
