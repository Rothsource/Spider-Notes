package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

func (s *Server) registerNotes(p func(string, http.HandlerFunc)) {
	p("GET /notes", s.folderPage)
	p("GET /notes/folder/{id}", s.folderPage)
	p("POST /notes/folders", s.folderCreate)
	p("POST /notes/folders/{id}/{action}", s.folderAction)
	p("POST /notes", s.noteCreate)
	p("POST /notes/upload", s.folderUpload)
	p("GET /notes/{id}", s.notePage)
	p("POST /notes/{id}", s.noteSave)
	p("POST /notes/{id}/{action}", s.noteAction)
	p("POST /preview", s.preview)
	p("POST /attachments/upload", s.attachmentUpload)
	p("GET /attachments/{id}/{name}", s.attachmentServe)
	p("POST /attachments/{id}/{action}", s.attachmentAction)
}

func formID(r *http.Request, k string) *int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(r.FormValue(k)), 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

func folderURL(id *int64) string {
	if id == nil {
		return "/notes"
	}
	return "/notes/folder/" + strconv.FormatInt(*id, 10)
}

// ---- folder view ----

func (s *Server) folderPage(w http.ResponseWriter, r *http.Request) {
	var id int64
	if r.PathValue("id") != "" {
		var err error
		if id, err = pathID(r); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	c, err := s.svc.Contents(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tree, flat, err := s.svc.FolderTree(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	title := "Folders"
	if c.Folder != nil {
		title = c.Folder.Name
	}
	s.render(w, r, "notes", page{"Title": title, "C": c, "Tree": tree, "Flat": flat, "Cur": id, "MaxMB": s.svc.MaxUploadBytes() >> 20})
}

func (s *Server) folderCreate(w http.ResponseWriter, r *http.Request) {
	parent := formID(r, "parent_id")
	f, err := s.svc.CreateFolder(r.Context(), r.FormValue("name"), parent)
	if err != nil {
		s.failBack(w, r, err, folderURL(parent))
		return
	}
	redirect(w, r, folderURL(&f.ID), "Folder created")
}

func (s *Server) folderAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	back := safeBack(r, "/notes")
	var f *model.Folder
	switch r.PathValue("action") {
	case "rename":
		f, err = s.svc.RenameFolder(ctx, id, r.FormValue("name"))
		if err == nil {
			redirect(w, r, back, "Folder renamed to “"+f.Name+"”")
			return
		}
	case "move":
		if f, err = s.svc.MoveFolder(ctx, id, formID(r, "parent_id")); err == nil {
			redirect(w, r, folderURL(&f.ID), "Folder moved")
			return
		}
	case "delete":
		var parent *int64
		if cur, e := s.svc.GetFolder(ctx, id); e == nil {
			parent = cur.ParentID
		}
		if err = s.svc.DeleteFolder(ctx, id); err == nil {
			redirect(w, r, folderURL(parent), "Folder deleted")
			return
		}
	default:
		err = model.ErrNotFound
	}
	s.failBack(w, r, err, back)
}

// failBack redirects with the validation message, or fails normally.
func (s *Server) failBack(w http.ResponseWriter, r *http.Request, err error, back string) {
	var ve *model.ValidationError
	if errors.As(err, &ve) {
		sep := "?"
		if strings.Contains(back, "?") {
			sep = "&"
		}
		redirect(w, r, back+sep+"err="+url.QueryEscape(ve.Msg), "")
		return
	}
	s.fail(w, r, err)
}

func (s *Server) folderUpload(w http.ResponseWriter, r *http.Request) {
	folder := formID(r, "folder_id")
	back := folderURL(folder)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.failBack(w, r, model.Invalid(uploadErr(err, s.svc.MaxUploadBytes())), back)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		s.failBack(w, r, model.Invalid("Choose at least one file"), back)
		return
	}
	owner := service.UploadOwner{}
	if folder != nil {
		owner.FolderID = *folder
	}
	// Upload every file that is valid; report the ones that failed.
	n := 0
	var failed []string
	for _, fh := range files {
		f, err := fh.Open()
		if err == nil {
			_, err = s.svc.SaveUpload(r.Context(), f, fh.Filename, owner)
			_ = f.Close()
		}
		if err != nil {
			var ve *model.ValidationError
			if !errors.As(err, &ve) {
				s.fail(w, r, err)
				return
			}
			failed = append(failed, service.SanitizeFilename(fh.Filename)+": "+ve.Msg)
			continue
		}
		n++
	}
	if len(failed) > 0 {
		msg := fmt.Sprintf("Uploaded %d of %d file(s). ", n, len(files)) + strings.Join(failed, "; ")
		s.failBack(w, r, model.Invalid(msg), back)
		return
	}
	redirect(w, r, back, fmt.Sprintf("Uploaded %d file(s)", n))
}

func uploadErr(err error, limit int64) string {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return fmt.Sprintf("Upload is too large (max %d MB)", limit>>20)
	}
	return "Upload failed: " + err.Error()
}

// ---- notes ----

func (s *Server) noteCreate(w http.ResponseWriter, r *http.Request) {
	n, err := s.svc.CreateNote(r.Context(), formID(r, "folder_id"), r.FormValue("title"), "")
	if err != nil {
		s.failBack(w, r, err, folderURL(formID(r, "folder_id")))
		return
	}
	redirect(w, r, "/notes/"+strconv.FormatInt(n.ID, 10)+"?edit=1", "")
}

func (s *Server) notePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	n, err := s.svc.GetNote(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var path []model.Folder
	if n.FolderID != nil {
		path, _ = s.svc.FolderPath(ctx, *n.FolderID)
	}
	files, _ := s.svc.ListAttachments(ctx, model.AttachmentFilter{NoteID: id})
	_, flat, _ := s.svc.FolderTree(ctx)
	s.render(w, r, "note", page{"Title": n.Title, "N": n, "FolderPath": path, "Files": files, "Flat": flat,
		"Edit": r.URL.Query().Get("edit") == "1"})
}

func (s *Server) noteSave(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.svc.UpdateNote(r.Context(), id, r.FormValue("title"), r.FormValue("content"))
	if err != nil {
		s.failBack(w, r, err, "/notes/"+strconv.FormatInt(id, 10)+"?edit=1")
		return
	}
	if isHX(r) { // background save (Ctrl+S): stay in the editor
		w.Header().Set("HX-Trigger", `{"saved":"Saved"}`)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	redirect(w, r, "/notes/"+strconv.FormatInt(n.ID, 10), "Saved")
}

func (s *Server) noteAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	self := "/notes/" + strconv.FormatInt(id, 10)
	var n *model.Note
	switch r.PathValue("action") {
	case "move":
		if n, err = s.svc.MoveNote(ctx, id, formID(r, "folder_id")); err == nil {
			redirect(w, r, safeBack(r, self), "Note moved")
			return
		}
	case "pin", "unpin":
		if _, err = s.svc.SetNotePinned(ctx, id, r.PathValue("action") == "pin"); err == nil {
			redirect(w, r, safeBack(r, self), "")
			return
		}
	case "rename":
		if n, err = s.svc.GetNote(ctx, id); err == nil {
			if _, err = s.svc.UpdateNote(ctx, id, r.FormValue("name"), n.ContentMD); err == nil {
				redirect(w, r, safeBack(r, self), "Note renamed")
				return
			}
		}
	case "toggle":
		idx, e := strconv.Atoi(r.URL.Query().Get("idx"))
		if e != nil {
			err = model.Invalid("bad checkbox index")
			break
		}
		if n, err = s.svc.ToggleNoteCheckbox(ctx, id, idx); err == nil {
			s.partial(w, r, "notebody", n)
			return
		}
	case "delete":
		var folder *int64
		if cur, e := s.svc.GetNote(ctx, id); e == nil {
			folder = cur.FolderID
		}
		if err = s.svc.DeleteNote(ctx, id); err == nil {
			redirect(w, r, folderURL(folder), "Note deleted")
			return
		}
	default:
		err = model.ErrNotFound
	}
	s.failBack(w, r, err, safeBack(r, self))
}

// ---- attachments ----

type uploadResult struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	Markdown string `json:"markdown"`
	IsImage  bool   `json:"is_image"`
	Size     int64  `json:"size"`
}

// attachmentUpload handles editor uploads (one file, JSON response).
func (s *Server) attachmentUpload(w http.ResponseWriter, r *http.Request) {
	writeErr := func(status int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		writeErr(http.StatusBadRequest, uploadErr(err, s.svc.MaxUploadBytes()))
		return
	}
	defer f.Close()
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	owner := service.UploadOwner{}
	if id := formID(r, "memo_id"); id != nil {
		owner.MemoID = *id
	}
	if id := formID(r, "note_id"); id != nil {
		owner.NoteID = *id
	}
	a, err := s.svc.SaveUpload(r.Context(), f, fh.Filename, owner)
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			writeErr(http.StatusBadRequest, ve.Msg)
			return
		}
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadResult{ID: a.ID, Filename: a.Filename, URL: service.AttachmentURL(a),
		Markdown: service.AttachmentMarkdown(a), IsImage: a.IsImage(), Size: a.Size})
}

// Types safe to display in the browser; everything else is downloaded.
func inlineSafe(mt string) bool {
	switch {
	case strings.HasPrefix(mt, "image/") && mt != "image/svg+xml":
		return true
	case strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "video/"):
		return true
	case mt == "application/pdf", mt == "text/plain", mt == "text/markdown", mt == "text/csv":
		return true
	}
	return false
}

// ServeAttachment writes a stored file with safe headers (used by web and API).
func ServeAttachment(w http.ResponseWriter, r *http.Request, svc *service.Service, id int64) {
	a, f, err := svc.OpenAttachment(r.Context(), id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	h := w.Header()
	disp := "attachment"
	ct := "application/octet-stream"
	if inlineSafe(a.Mime) && r.URL.Query().Get("download") != "1" {
		disp, ct = "inline", a.Mime
		if strings.HasPrefix(ct, "text/") {
			ct = "text/plain; charset=utf-8" // never rendered as HTML
		}
	}
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": a.Filename}))
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	if a.Mime != "application/pdf" { // the browser PDF viewer does not work in a sandbox
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	http.ServeContent(w, r, "", a.UpdatedAt, f) // supports Range (video, large files)
}

func (s *Server) attachmentServe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	ServeAttachment(w, r, s.svc, id)
}

func (s *Server) attachmentAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	back := safeBack(r, "/notes")
	switch r.PathValue("action") {
	case "rename":
		if _, err = s.svc.RenameAttachment(ctx, id, r.FormValue("name")); err == nil {
			redirect(w, r, back, "File renamed")
			return
		}
	case "move":
		folder := formID(r, "folder_id")
		if _, err = s.svc.MoveAttachment(ctx, id, folder); err == nil {
			redirect(w, r, folderURL(folder), "File moved")
			return
		}
	case "delete":
		if err = s.svc.DeleteAttachment(ctx, id); err == nil {
			redirect(w, r, back, "File deleted")
			return
		}
	default:
		err = model.ErrNotFound
	}
	s.failBack(w, r, err, back)
}

// preview renders Markdown for the editor's Preview tab.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(markdown.Render(r.FormValue("content"), false)))
}
