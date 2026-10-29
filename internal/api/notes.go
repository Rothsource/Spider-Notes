package api

import (
	"net/http"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// ServeFile streams an attachment (implemented by the web package to share safe headers).
var ServeFile func(w http.ResponseWriter, r *http.Request, svc *service.Service, id int64)

func (a *API) registerNotes(h func(string, func(http.ResponseWriter, *http.Request) (any, error)), protect func(http.Handler) http.Handler, mux *http.ServeMux) {
	const p = "/api/v1"
	h("GET "+p+"/folders", a.listFolders)
	h("POST "+p+"/folders", a.createFolder)
	h("GET "+p+"/folders/{id}", a.folderContents)
	h("PUT "+p+"/folders/{id}", a.updateFolder)
	h("DELETE "+p+"/folders/{id}", a.deleteFolder)

	h("GET "+p+"/notes", a.listNotes)
	h("POST "+p+"/notes", a.createNote)
	h("GET "+p+"/notes/{id}", a.getNote)
	h("PUT "+p+"/notes/{id}", a.updateNote)
	h("DELETE "+p+"/notes/{id}", a.deleteNote)

	h("GET "+p+"/attachments", a.listAttachments)
	h("POST "+p+"/attachments", a.uploadAttachment)
	h("GET "+p+"/attachments/{id}", a.getAttachment)
	h("DELETE "+p+"/attachments/{id}", a.deleteAttachment)
	mux.Handle("GET "+p+"/attachments/{id}/content", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil || ServeFile == nil {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		ServeFile(w, r, a.svc, id)
	})))
}

func idPtr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// ---- folders ----

func (a *API) listFolders(_ http.ResponseWriter, r *http.Request) (any, error) {
	_, flat, err := a.svc.FolderTree(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(flat))
	for _, n := range flat {
		out = append(out, map[string]any{"id": n.Folder.ID, "parent_id": n.Folder.ParentID, "name": n.Folder.Name, "path": n.Path, "depth": n.Depth,
			"created_at": n.Folder.CreatedAt, "updated_at": n.Folder.UpdatedAt})
	}
	return map[string]any{"folders": out}, nil
}

type folderBody struct {
	Name     *string `json:"name"`
	ParentID *int64  `json:"parent_id"`
}

func (a *API) createFolder(_ http.ResponseWriter, r *http.Request) (any, error) {
	var b folderBody
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	name := ""
	if b.Name != nil {
		name = *b.Name
	}
	return a.svc.CreateFolder(r.Context(), name, b.ParentID)
}

// folderContents lists a folder; id 0 means the top level.
func (a *API) folderContents(_ http.ResponseWriter, r *http.Request) (any, error) {
	if r.PathValue("id") == "0" {
		return a.svc.Contents(r.Context(), 0)
	}
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return a.svc.Contents(r.Context(), id)
}

func (a *API) updateFolder(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var b struct {
		Name     *string `json:"name"`
		ParentID *int64  `json:"parent_id"`
		Move     bool    `json:"move"`
	}
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	var f *model.Folder
	if b.Name != nil {
		if f, err = a.svc.RenameFolder(r.Context(), id, *b.Name); err != nil {
			return nil, err
		}
	}
	if b.Move || b.ParentID != nil {
		if f, err = a.svc.MoveFolder(r.Context(), id, b.ParentID); err != nil {
			return nil, err
		}
	}
	if f == nil {
		return a.svc.GetFolder(r.Context(), id)
	}
	return f, nil
}

func (a *API) deleteFolder(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteFolder(r.Context(), id)
}

// ---- notes ----

func (a *API) listNotes(_ http.ResponseWriter, r *http.Request) (any, error) {
	if r.URL.Query().Has("folder_id") {
		c, err := a.svc.Contents(r.Context(), queryInt(r, "folder_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"notes": nonNil(c.Notes)}, nil
	}
	notes, err := a.svc.ListAllNotes(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"notes": nonNil(notes)}, nil
}

type noteBody struct {
	Title     *string `json:"title"`
	ContentMD *string `json:"content_md"`
	FolderID  *int64  `json:"folder_id"`
	Pinned    *bool   `json:"pinned"`
}

func (a *API) createNote(_ http.ResponseWriter, r *http.Request) (any, error) {
	var b noteBody
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	title, content := "", ""
	if b.Title != nil {
		title = *b.Title
	}
	if b.ContentMD != nil {
		content = *b.ContentMD
	}
	return a.svc.CreateNote(r.Context(), b.FolderID, title, content)
}

func (a *API) getNote(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return a.svc.GetNote(r.Context(), id)
}

func (a *API) updateNote(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var b noteBody
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	ctx := r.Context()
	n, err := a.svc.GetNote(ctx, id)
	if err != nil {
		return nil, err
	}
	if b.Title != nil || b.ContentMD != nil {
		title, content := n.Title, n.ContentMD
		if b.Title != nil {
			title = *b.Title
		}
		if b.ContentMD != nil {
			content = *b.ContentMD
		}
		if n, err = a.svc.UpdateNote(ctx, id, title, content); err != nil {
			return nil, err
		}
	}
	if b.FolderID != nil {
		if n, err = a.svc.MoveNote(ctx, id, idPtr(*b.FolderID)); err != nil {
			return nil, err
		}
	}
	if b.Pinned != nil {
		if n, err = a.svc.SetNotePinned(ctx, id, *b.Pinned); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func (a *API) deleteNote(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteNote(r.Context(), id)
}

// ---- attachments ----

type attachmentOut struct {
	*model.Attachment
	URL      string `json:"url"`
	Markdown string `json:"markdown"`
}

func outAtt(at *model.Attachment) attachmentOut {
	return attachmentOut{Attachment: at, URL: "/api/v1/attachments/" + itoa(at.ID) + "/content", Markdown: service.AttachmentMarkdown(at)}
}

func (a *API) listAttachments(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	f := model.AttachmentFilter{MemoID: queryInt(r, "memo_id"), NoteID: queryInt(r, "note_id")}
	if q.Has("folder_id") || (f.MemoID == 0 && f.NoteID == 0) {
		f.InFolder, f.FolderID = true, queryInt(r, "folder_id")
	}
	list, err := a.svc.ListAttachments(r.Context(), f)
	if err != nil {
		return nil, err
	}
	out := make([]attachmentOut, len(list))
	for i := range list {
		out[i] = outAtt(&list[i])
	}
	return map[string]any{"attachments": out}, nil
}

func (a *API) uploadAttachment(_ http.ResponseWriter, r *http.Request) (any, error) {
	f, fh, err := r.FormFile("file")
	if err != nil {
		return nil, model.Invalid("multipart field 'file' is required")
	}
	defer f.Close()
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	owner := service.UploadOwner{FolderID: formInt(r, "folder_id"), MemoID: formInt(r, "memo_id"), NoteID: formInt(r, "note_id")}
	at, err := a.svc.SaveUpload(r.Context(), f, fh.Filename, owner)
	if err != nil {
		return nil, err
	}
	return outAtt(at), nil
}

func (a *API) getAttachment(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	at, err := a.svc.GetAttachment(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return outAtt(at), nil
}

func (a *API) deleteAttachment(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteAttachment(r.Context(), id)
}
