package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sovannroth/mynotes/internal/model"
)

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func withFiles(e *env) {
	e.svc.attachDir = filepath.Join(e.t.TempDir(), "attachments")
	e.svc.maxUpload = 1 << 20
}

func TestFoldersAndNotes(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	work, err := e.svc.CreateFolder(ctx, "Work", nil)
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := e.svc.CreateFolder(ctx, "Projects", &work.ID)
	deep, _ := e.svc.CreateFolder(ctx, "Spider", &proj.ID)

	// Validation: duplicates (case-insensitive), bad names, cycles.
	if _, err := e.svc.CreateFolder(ctx, "work", nil); !errors.Is(err, model.ErrValidation) {
		t.Fatal("duplicate sibling name should fail")
	}
	if _, err := e.svc.CreateFolder(ctx, "a/b", nil); !errors.Is(err, model.ErrValidation) {
		t.Fatal("slash in name should fail")
	}
	if _, err := e.svc.MoveFolder(ctx, work.ID, &deep.ID); !errors.Is(err, model.ErrValidation) {
		t.Fatal("moving a folder into its descendant must fail")
	}
	path, _ := e.svc.FolderPath(ctx, deep.ID)
	if len(path) != 3 || path[0].Name != "Work" || path[2].Name != "Spider" {
		t.Fatalf("path %+v", path)
	}

	n, err := e.svc.CreateNote(ctx, &proj.ID, "", "")
	if err != nil || n.Title != "Untitled" {
		t.Fatalf("create note: %v %+v", err, n)
	}
	n, err = e.svc.UpdateNote(ctx, n.ID, "Launch plan", "# Plan\n- [ ] write spec #work\n- [ ] ship")
	if err != nil || len(n.Tags) != 1 {
		t.Fatalf("update note: %v %+v", err, n)
	}
	if n, err = e.svc.ToggleNoteCheckbox(ctx, n.ID, 1); err != nil || !strings.Contains(n.ContentMD, "- [x] ship") {
		t.Fatalf("toggle: %v", err)
	}
	hits, _ := e.svc.Search(ctx, "launch", 10)
	if len(hits) != 1 || hits[0].Kind != "note" || hits[0].Title != "Launch plan" {
		t.Fatalf("search notes: %+v", hits)
	}
	c, _ := e.svc.Contents(ctx, proj.ID)
	if len(c.Folders) != 1 || len(c.Notes) != 1 || len(c.Path) != 2 {
		t.Fatalf("contents %+v", c)
	}
	_, flat, _ := e.svc.FolderTree(ctx)
	if len(flat) != 3 || flat[2].Path != "Work / Projects / Spider" {
		t.Fatalf("tree %+v", flat)
	}
	// Move the note to the top level, then delete the Work folder recursively.
	if n, err = e.svc.MoveNote(ctx, n.ID, nil); err != nil || n.FolderID != nil {
		t.Fatalf("move note: %v", err)
	}
	inner, _ := e.svc.CreateNote(ctx, &deep.ID, "Inside", "x")
	if err := e.svc.DeleteFolder(ctx, work.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetFolder(ctx, deep.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("subfolders should be deleted")
	}
	if _, err := e.svc.GetNote(ctx, inner.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("notes inside should be deleted")
	}
	if _, err := e.svc.GetNote(ctx, n.ID); err != nil {
		t.Fatal("moved-out note must survive")
	}
}

func TestUploads(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	withFiles(e)
	ctx := context.Background()
	f, _ := e.svc.CreateFolder(ctx, "Docs", nil)

	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 100)...)
	a, err := e.svc.SaveUpload(ctx, bytes.NewReader(png), `..\..\evil/../photo.PNG`, UploadOwner{FolderID: f.ID})
	if err != nil {
		t.Fatal(err)
	}
	if a.Filename != "photo.PNG" || a.Mime != "image/png" || !a.IsImage() || a.Size != int64(len(png)) || len(a.SHA256) != 64 {
		t.Fatalf("attachment %+v", a)
	}
	if strings.Contains(a.StorageKey, "photo") || strings.Contains(a.StorageKey, "..") {
		t.Fatalf("storage key must not use the user's filename: %s", a.StorageKey)
	}
	got, file, err := e.svc.OpenAttachment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(file)
	file.Close()
	if !bytes.Equal(b, png) || got.ID != a.ID {
		t.Fatal("content mismatch")
	}

	// HTML disguised as .txt is sniffed as HTML (served as a download later).
	h, _ := e.svc.SaveUpload(ctx, strings.NewReader("<html><script>alert(1)</script>"), "notes.txt", UploadOwner{})
	if !strings.HasPrefix(h.Mime, "text/html") {
		t.Fatalf("sniffed mime %s", h.Mime)
	}
	md, _ := e.svc.SaveUpload(ctx, strings.NewReader("# hello"), "readme.md", UploadOwner{})
	if md.Mime != "text/markdown" {
		t.Fatalf("markdown mime %s", md.Mime)
	}

	// Limits.
	if _, err := e.svc.SaveUpload(ctx, bytes.NewReader(make([]byte, 2<<20)), "big.bin", UploadOwner{}); !errors.Is(err, model.ErrValidation) {
		t.Fatal("oversized upload must fail")
	}
	if _, err := e.svc.SaveUpload(ctx, strings.NewReader(""), "empty.txt", UploadOwner{}); !errors.Is(err, model.ErrValidation) {
		t.Fatal("empty upload must fail")
	}

	// An editor upload is claimed by the memo that references it.
	img, _ := e.svc.SaveUpload(ctx, bytes.NewReader(png), "cat.png", UploadOwner{})
	m, err := e.svc.CreateMemo(ctx, "Look "+AttachmentMarkdown(img))
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListAttachments(ctx, model.AttachmentFilter{MemoID: m.ID}); len(list) != 1 || list[0].ID != img.ID {
		t.Fatalf("memo attachments %+v", list)
	}
	if !strings.HasPrefix(AttachmentMarkdown(img), "![cat.png](/attachments/") {
		t.Fatalf("markdown %s", AttachmentMarkdown(img))
	}

	// Export includes notes (with folder path) and files.
	if _, err := e.svc.CreateNote(ctx, &f.ID, "Read: me?", "body"); err != nil {
		t.Fatal(err)
	}
	files, err := e.svc.BuildExport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["notes/Docs/Read_ me_.md"]) != "# Read: me?\n\nbody\n" {
		t.Fatalf("note export missing: %v", keys(files))
	}
	var zbuf bytes.Buffer
	if err := e.svc.ExportZip(ctx, &zbuf); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(zbuf.Bytes()), int64(zbuf.Len()))
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	if !names["files/Docs/photo.PNG"] || !names["attachments/"+strconv.FormatInt(img.ID, 10)+"/cat.png"] {
		t.Fatalf("zip entries: %v", names)
	}

	// Rename, move to top level, delete.
	if a, err = e.svc.RenameAttachment(ctx, a.ID, "holiday/../x.png"); err != nil || a.Filename != "x.png" {
		t.Fatalf("rename %v %+v", err, a)
	}
	if a, err = e.svc.MoveAttachment(ctx, a.ID, nil); err != nil || a.FolderID != nil {
		t.Fatalf("move %v", err)
	}
	if err := e.svc.DeleteAttachment(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.OpenAttachment(ctx, a.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("deleted file should not open")
	}
}
