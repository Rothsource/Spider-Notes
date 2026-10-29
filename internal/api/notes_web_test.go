package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (a *testApp) upload(path, csrf string, fields map[string]string, field, name string, content []byte) *http.Response {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(field, name)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", a.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	return resp
}

func TestFoldersNotesAndFilesWeb(t *testing.T) {
	a := newApp(t)
	csrf := a.login()

	// Create a folder and a note inside it.
	code, _, h := a.postForm("/notes/folders", url.Values{"csrf_token": {csrf}, "name": {"Work"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/notes/folder/1?msg=Folder+created" {
		t.Fatalf("create folder: %d %v", code, h.Get("Location"))
	}
	code, _, h = a.postForm("/notes", url.Values{"csrf_token": {csrf}, "folder_id": {"1"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/notes/1?edit=1" {
		t.Fatalf("create note: %d %v", code, h.Get("Location"))
	}
	code, _, _ = a.postForm("/notes/1", url.Values{"csrf_token": {csrf}, "title": {"Plan"}, "content": {"# Goals\n- [ ] ship #work"}})
	if code != http.StatusSeeOther {
		t.Fatalf("save note: %d", code)
	}
	// Duplicate folder name shows an error banner instead of failing hard.
	_, _, h = a.postForm("/notes/folders", url.Values{"csrf_token": {csrf}, "name": {"work"}})
	if !strings.Contains(h.Get("Location"), "err=") {
		t.Fatalf("duplicate folder should redirect with err: %v", h.Get("Location"))
	}

	// Editor upload (JSON) attached to the note.
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	resp := a.upload("/attachments/upload", csrf, map[string]string{"note_id": "1"}, "file", "diagram.png", png)
	var up map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&up)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(up["markdown"].(string), "![diagram.png](/attachments/1/diagram.png)") {
		t.Fatalf("editor upload: %d %v", resp.StatusCode, up)
	}
	// Folder upload (form, multiple files) including a disguised HTML file.
	resp = a.upload("/notes/upload", csrf, map[string]string{"folder_id": "1", "csrf_token": csrf}, "files", "page.txt", []byte("<html><script>alert(1)</script></html>"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("folder upload: %d", resp.StatusCode)
	}

	// Pages render.
	for _, p := range []string{"/notes", "/notes/folder/1", "/notes/1", "/notes/1?edit=1", "/memos", "/search?q=goals"} {
		code, body := a.get(p)
		if code != 200 || strings.Contains(body, "ZgotmplZ") || strings.Contains(body, "<no value>") {
			t.Fatalf("%s: %d", p, code)
		}
	}
	_, folder := a.get("/notes/folder/1")
	for _, want := range []string{"Plan", "page.txt", "Move to"} {
		if !strings.Contains(folder, want) {
			t.Fatalf("folder page missing %q", want)
		}
	}
	_, search := a.get("/search?q=goals")
	if !strings.Contains(search, `href="/notes/1"`) {
		t.Fatal("note not found by search")
	}
	_, nav := a.get("/")
	if !strings.Contains(nav, "<span>Spider Notes</span></a>") || !strings.Contains(nav, "<span>Folders</span>") {
		t.Fatal("sidebar should show Spider Notes and Folders")
	}

	// Serving: images inline; HTML forced to download with a sandbox CSP.
	get := func(p string) *http.Response {
		r, err := a.client.Get(a.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(r.Body)
		r.Body.Close()
		return r
	}
	r := get("/attachments/1/diagram.png")
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("png: %d %v", r.StatusCode, r.Header)
	}
	r = get("/attachments/2/page.txt")
	if r.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("html must be a sandboxed download: %v", r.Header)
	}
	// Unauthenticated access is refused.
	anon, _ := http.Get(a.srv.URL + "/attachments/1/diagram.png")
	anon.Body.Close()
	if anon.Request.URL.Path != "/login" {
		t.Fatalf("attachments must require login, ended at %s", anon.Request.URL.Path)
	}

	// Rename and delete the folder (recursive).
	code, _, _ = a.postForm("/notes/folders/1/rename", url.Values{"csrf_token": {csrf}, "name": {"Office"}, "back": {"/notes"}})
	if code != http.StatusSeeOther {
		t.Fatalf("rename: %d", code)
	}
	code, _, _ = a.postForm("/notes/folders/1/delete", url.Values{"csrf_token": {csrf}})
	if code != http.StatusSeeOther {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := a.get("/notes/1"); code != http.StatusNotFound {
		t.Fatalf("note inside deleted folder should be gone: %d", code)
	}
}

func TestNotesAPI(t *testing.T) {
	a := newApp(t)
	tok, _, err := a.am.CreateToken(t.Context(), "t")
	if err != nil {
		t.Fatal(err)
	}
	code, f := a.apiCall("POST", "/folders", tok, map[string]any{"name": "Recipes"})
	if code != http.StatusCreated {
		t.Fatalf("folder: %d %v", code, f)
	}
	code, n := a.apiCall("POST", "/notes", tok, map[string]any{"title": "Amok", "content_md": "Fish curry #khmer", "folder_id": f["id"]})
	if code != http.StatusCreated || n["tags"].([]any)[0] != "khmer" {
		t.Fatalf("note: %d %v", code, n)
	}
	if code, c := a.apiCall("GET", "/folders/1", tok, nil); code != 200 || len(c["notes"].([]any)) != 1 {
		t.Fatalf("contents: %d %v", code, c)
	}
	if code, u := a.apiCall("PUT", "/notes/1", tok, map[string]any{"folder_id": 0, "pinned": true}); code != 200 || u["folder_id"] != nil || u["pinned"] != true {
		t.Fatalf("move/pin: %d %v", code, u)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("folder_id", "1")
	fw, _ := mw.CreateFormFile("file", "list.csv")
	_, _ = fw.Write([]byte("a,b\n1,2\n"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", a.srv.URL+"/api/v1/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var att map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&att)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || att["mime"] != "text/csv" {
		t.Fatalf("upload: %d %v", resp.StatusCode, att)
	}
	req, _ = http.NewRequest("GET", a.srv.URL+att["url"].(string), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ = http.DefaultClient.Do(req)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "a,b\n1,2\n" {
		t.Fatalf("download: %d %q", resp.StatusCode, body)
	}
	if code, l := a.apiCall("GET", "/attachments?folder_id=1", tok, nil); code != 200 || len(l["attachments"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, l)
	}
	if code, _ := a.apiCall("DELETE", "/folders/1", tok, nil); code != http.StatusNoContent {
		t.Fatalf("delete folder: %d", code)
	}
}
