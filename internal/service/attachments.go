package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

// UploadOwner says where a new file goes. All zero = top-level files.
type UploadOwner struct {
	FolderID int64
	MemoID   int64
	NoteID   int64
}

// MaxUploadBytes returns the configured upload limit.
func (s *Service) MaxUploadBytes() int64 { return s.maxUpload }

// SanitizeFilename keeps a safe display name (no paths or control characters).
func SanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if r := []rune(name); len(r) > 200 {
		ext := filepath.Ext(name)
		name = string(r[:200-len([]rune(ext))]) + ext
	}
	if name == "" {
		name = "file"
	}
	return name
}

var extRe = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

// Common types missing from Go's built-in table (minimal images have no /etc/mime.types).
func init() {
	for ext, t := range map[string]string{
		".md": "text/markdown", ".markdown": "text/markdown", ".txt": "text/plain", ".csv": "text/csv",
		".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".odt":  "application/vnd.oasis.opendocument.text", ".zip": "application/zip", ".7z": "application/x-7z-compressed",
		".heic": "image/heic", ".mp4": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
		".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".ogg": "audio/ogg", ".wav": "audio/wav",
	} {
		_ = mime.AddExtensionType(ext, t)
	}
}

// SaveUpload stores a file and records it. The content is streamed to disk with
// a size limit; the type is sniffed from the bytes (falling back to the extension).
func (s *Service) SaveUpload(ctx context.Context, src io.Reader, filename string, owner UploadOwner) (*model.Attachment, error) {
	if s.attachDir == "" {
		return nil, errors.New("attachment storage is not configured")
	}
	filename = SanitizeFilename(filename)
	now := s.now().UTC()
	sub := now.Format("2006/01")
	dir := filepath.Join(s.attachDir, filepath.FromSlash(sub))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	h := sha256.New()
	head := make([]byte, 0, 512)
	limit := s.maxUpload
	n, err := io.Copy(io.MultiWriter(tmp, h, &headWriter{buf: &head}), io.LimitReader(src, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	if n > limit {
		cleanup()
		return nil, model.Invalid(fmt.Sprintf("File is too large (max %d MB)", limit>>20))
	}
	if n == 0 {
		cleanup()
		return nil, model.Invalid("File is empty")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if !extRe.MatchString(ext) {
		ext = ""
	}
	mt := detectMime(head, ext)
	key := sub + "/" + randomHex(16) + ext
	final := filepath.Join(s.attachDir, filepath.FromSlash(key))
	if err := os.Rename(tmpName, final); err != nil {
		cleanup()
		return nil, err
	}

	a := &model.Attachment{Filename: filename, Mime: mt, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), StorageKey: key, CreatedAt: now, UpdatedAt: now}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		switch {
		case owner.NoteID > 0:
			if _, err := tx.GetNote(ctx, owner.NoteID); err != nil {
				return model.Invalid("Note not found")
			}
			a.NoteID = ptr(owner.NoteID)
		case owner.MemoID > 0:
			if _, err := tx.GetMemo(ctx, owner.MemoID); err != nil {
				return model.Invalid("Memo not found")
			}
			a.MemoID = ptr(owner.MemoID)
		case owner.FolderID > 0:
			if _, err := tx.GetFolder(ctx, owner.FolderID); err != nil {
				return model.Invalid("Folder not found")
			}
			a.FolderID = ptr(owner.FolderID)
		}
		return tx.CreateAttachment(ctx, a)
	})
	if err != nil {
		_ = os.Remove(final)
		return nil, err
	}
	return a, nil
}

type headWriter struct{ buf *[]byte }

func (w *headWriter) Write(p []byte) (int, error) {
	if room := cap(*w.buf) - len(*w.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		*w.buf = append(*w.buf, p[:room]...)
	}
	return len(p), nil
}

func detectMime(head []byte, ext string) string {
	sniffed := http.DetectContentType(head)
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = sniffed[:i]
	}
	byExt := ""
	if ext != "" {
		byExt = mime.TypeByExtension(ext)
		if i := strings.IndexByte(byExt, ';'); i >= 0 {
			byExt = byExt[:i]
		}
	}
	// Generic sniff results are refined by the extension (e.g. .md, .docx, .csv).
	if (sniffed == "application/octet-stream" || sniffed == "text/plain" || sniffed == "application/zip") && byExt != "" {
		return byExt
	}
	return sniffed
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// OpenAttachment returns the attachment and an open file for reading.
func (s *Service) OpenAttachment(ctx context.Context, id int64) (*model.Attachment, *os.File, error) {
	a, err := s.st.GetAttachment(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	p := filepath.Join(s.attachDir, filepath.FromSlash(a.StorageKey))
	if rel, err := filepath.Rel(s.attachDir, p); err != nil || strings.HasPrefix(rel, "..") {
		return nil, nil, model.ErrNotFound
	}
	f, err := os.Open(p) //nolint:gosec // path built from a server-generated key, checked above
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, model.ErrNotFound
		}
		return nil, nil, err
	}
	return a, f, nil
}

// GetAttachment returns attachment metadata.
func (s *Service) GetAttachment(ctx context.Context, id int64) (*model.Attachment, error) {
	return s.st.GetAttachment(ctx, id)
}

// ListAttachments lists attachments.
func (s *Service) ListAttachments(ctx context.Context, f model.AttachmentFilter) ([]model.Attachment, error) {
	return s.st.ListAttachments(ctx, f)
}

// RenameAttachment changes a file's display name.
func (s *Service) RenameAttachment(ctx context.Context, id int64, name string) (*model.Attachment, error) {
	name = SanitizeFilename(name)
	return s.modifyAttachment(ctx, id, func(_ store.Store, a *model.Attachment) error {
		a.Filename = name
		return nil
	})
}

// MoveAttachment moves a file into a folder (nil = top level). A file attached
// to a memo or note becomes a standalone file in that folder.
func (s *Service) MoveAttachment(ctx context.Context, id int64, folderID *int64) (*model.Attachment, error) {
	folderID = normParent(folderID)
	return s.modifyAttachment(ctx, id, func(tx store.Store, a *model.Attachment) error {
		if err := s.requireFolder(ctx, tx, folderID); err != nil {
			return err
		}
		a.FolderID, a.MemoID, a.NoteID = folderID, nil, nil
		return nil
	})
}

func (s *Service) modifyAttachment(ctx context.Context, id int64, fn func(store.Store, *model.Attachment) error) (*model.Attachment, error) {
	var a *model.Attachment
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if a, err = tx.GetAttachment(ctx, id); err != nil {
			return err
		}
		if err := fn(tx, a); err != nil {
			return err
		}
		a.UpdatedAt = s.now().UTC()
		return tx.UpdateAttachment(ctx, a)
	})
	return a, err
}

// DeleteAttachment soft-deletes a file (the bytes stay on disk, like all history).
func (s *Service) DeleteAttachment(ctx context.Context, id int64) error {
	return s.st.SoftDeleteAttachment(ctx, id, s.now().UTC())
}

var attachRefRe = regexp.MustCompile(`/attachments/([0-9]{1,18})/`)

// linkAttachments claims files referenced in Markdown that were uploaded
// before their memo/note existed (unowned uploads).
func (s *Service) linkAttachments(ctx context.Context, tx store.Store, content string, memoID, noteID int64) error {
	for _, m := range attachRefRe.FindAllStringSubmatch(content, 50) {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		a, err := tx.GetAttachment(ctx, id)
		if err != nil || a.FolderID != nil || a.MemoID != nil || a.NoteID != nil {
			continue
		}
		if memoID > 0 {
			a.MemoID = ptr(memoID)
		} else {
			a.NoteID = ptr(noteID)
		}
		a.UpdatedAt = s.now().UTC()
		if err := tx.UpdateAttachment(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// AttachmentURL is the in-app URL of a file.
func AttachmentURL(a *model.Attachment) string {
	return "/attachments/" + strconv.FormatInt(a.ID, 10) + "/" + urlPathEscape(a.Filename)
}

// AttachmentMarkdown is the snippet inserted into an editor for a file.
func AttachmentMarkdown(a *model.Attachment) string {
	label := strings.NewReplacer("[", "(", "]", ")").Replace(a.Filename)
	if a.IsImage() {
		return "![" + label + "](" + AttachmentURL(a) + ")"
	}
	return "[📎 " + label + "](" + AttachmentURL(a) + ")"
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
