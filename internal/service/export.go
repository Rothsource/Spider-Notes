package service

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
)

// BuildExport renders the Markdown export as path -> content.
// The DB stays the source of truth; this is read-only output.
func (s *Service) BuildExport(ctx context.Context) (map[string][]byte, error) {
	files := map[string][]byte{}

	// memos/YYYY-MM-DD.md
	memos, err := s.st.ListMemos(ctx, model.MemoFilter{Limit: 5000}, false)
	if err != nil {
		return nil, err
	}
	byDay := map[string][]model.Memo{}
	for _, m := range memos {
		d := m.CreatedAt.In(s.loc).Format("2006-01-02")
		byDay[d] = append(byDay[d], m)
	}
	for day, ms := range byDay {
		sort.Slice(ms, func(i, j int) bool { return ms[i].CreatedAt.Before(ms[j].CreatedAt) })
		var b strings.Builder
		fmt.Fprintf(&b, "# Memos %s\n", day)
		for _, m := range ms {
			fmt.Fprintf(&b, "\n## %s", m.CreatedAt.In(s.loc).Format("15:04"))
			if m.Pinned {
				b.WriteString(" 📌")
			}
			b.WriteString("\n\n" + strings.TrimSpace(m.ContentMD) + "\n")
		}
		files["memos/"+day+".md"] = []byte(b.String())
	}

	// history/YYYY-MM-DD.md
	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	hist, err := s.History(ctx, HistoryFilter{From: from, To: s.now().UTC().Add(time.Hour)})
	if err != nil {
		return nil, err
	}
	hDay := map[string][]model.Occurrence{}
	for _, o := range hist {
		d := OutcomeTime(&o).In(s.loc).Format("2006-01-02")
		hDay[d] = append(hDay[d], o)
	}
	for day, list := range hDay {
		sort.Slice(list, func(i, j int) bool { return list[i].ScheduledFor.Before(list[j].ScheduledFor) })
		var b strings.Builder
		fmt.Fprintf(&b, "# History %s\n\n", day)
		for _, o := range list {
			box := "[ ]"
			if o.Status == model.OccDone {
				box = "[x]"
			}
			fmt.Fprintf(&b, "- %s %s %s — %s", box, s.hm(o.ScheduledFor), o.Title, o.Status)
			if o.DoneAt != nil {
				fmt.Fprintf(&b, " at %s", s.hm(*o.DoneAt))
			}
			if o.ActionSource != "" && o.ActionSource != model.SourceSystem {
				fmt.Fprintf(&b, " (%s)", o.ActionSource)
			}
			if o.SentCount > 1 {
				fmt.Fprintf(&b, ", %d reminders sent", o.SentCount)
			}
			b.WriteString("\n")
		}
		files["history/"+day+".md"] = []byte(b.String())
	}

	// tasks.md
	tasks, err := s.st.ListTasks(ctx, model.TaskFilter{Status: model.TaskPending, Limit: 1000})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Open tasks\n\n_Exported %s_\n\n", s.Now().Format("2006-01-02 15:04 MST"))
	for _, t := range tasks {
		fmt.Fprintf(&b, "- [ ] %s", t.Title)
		if t.DueAt != nil {
			fmt.Fprintf(&b, " — due %s", t.DueAt.In(s.loc).Format("2006-01-02 15:04"))
		}
		if t.Priority == model.PriorityHigh {
			b.WriteString(" ‼️")
		}
		if t.RepeatRule != "" {
			b.WriteString(" 🔁 " + DescribeRule(t.RepeatRule, ""))
		}
		for _, tag := range t.Tags {
			b.WriteString(" #" + tag)
		}
		b.WriteString("\n")
		if d := strings.TrimSpace(t.DescriptionMD); d != "" {
			for _, l := range strings.Split(d, "\n") {
				b.WriteString("  " + l + "\n")
			}
		}
	}
	files["tasks.md"] = []byte(b.String())

	// notes/<folder path>/<title>.md
	notes, err := s.st.ListNotes(ctx, 0, true)
	if err != nil {
		return nil, err
	}
	paths, err := s.folderPaths(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		dir := "notes"
		if n.FolderID != nil {
			dir += "/" + paths[*n.FolderID]
		}
		name := uniqueName(files, dir+"/"+safeSegment(n.Title), ".md")
		files[name] = []byte("# " + n.Title + "\n\n" + strings.TrimSpace(n.ContentMD) + "\n")
	}
	return files, nil
}

// folderPaths maps folder id -> "A/B/C" using filesystem-safe names.
func (s *Service) folderPaths(ctx context.Context) (map[int64]string, error) {
	all, err := s.st.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]model.Folder{}
	for _, f := range all {
		byID[f.ID] = f
	}
	out := map[int64]string{}
	for _, f := range all {
		var parts []string
		cur, ok := f, true
		for i := 0; ok && i <= maxFolderDeep; i++ {
			parts = append([]string{safeSegment(cur.Name)}, parts...)
			if cur.ParentID == nil {
				break
			}
			cur, ok = byID[*cur.ParentID]
		}
		out[f.ID] = strings.Join(parts, "/")
	}
	return out, nil
}

func safeSegment(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	s = strings.Trim(s, ". ")
	if s == "" {
		s = "untitled"
	}
	return s
}

func uniqueName(taken map[string][]byte, base, ext string) string {
	name := base + ext
	for i := 2; ; i++ {
		if _, ok := taken[name]; !ok {
			return name
		}
		name = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
}

type exportFile struct {
	path string
	att  model.Attachment
}

// exportAttachments lists uploaded files: folder files keep their folder path,
// memo/note attachments go to attachments/<id>-<name>.
func (s *Service) exportAttachments(ctx context.Context) ([]exportFile, error) {
	paths, err := s.folderPaths(ctx)
	if err != nil {
		return nil, err
	}
	var out []exportFile
	seen := map[string][]byte{}
	add := func(list []model.Attachment, dirFor func(a model.Attachment) string) {
		for _, a := range list {
			dir := dirFor(a)
			ext := filepath.Ext(a.Filename)
			p := uniqueName(seen, dir+"/"+safeSegment(strings.TrimSuffix(a.Filename, ext)), ext)
			seen[p] = nil
			out = append(out, exportFile{path: p, att: a})
		}
	}
	top, err := s.st.ListAttachments(ctx, model.AttachmentFilter{InFolder: true, FolderID: 0, Limit: 5000})
	if err != nil {
		return nil, err
	}
	add(top, func(model.Attachment) string { return "files" })
	for id, p := range paths {
		list, err := s.st.ListAttachments(ctx, model.AttachmentFilter{InFolder: true, FolderID: id, Limit: 5000})
		if err != nil {
			return nil, err
		}
		dir := "files/" + p
		add(list, func(model.Attachment) string { return dir })
	}
	for _, kind := range []string{"memo", "note"} {
		owned, err := s.ownedAttachments(ctx, kind)
		if err != nil {
			return nil, err
		}
		add(owned, func(a model.Attachment) string { return "attachments/" + strconv.FormatInt(a.ID, 10) })
	}
	return out, nil
}

func (s *Service) ownedAttachments(ctx context.Context, kind string) ([]model.Attachment, error) {
	var out []model.Attachment
	if kind == "memo" {
		memos, err := s.st.ListMemos(ctx, model.MemoFilter{Limit: 5000}, false)
		if err != nil {
			return nil, err
		}
		for _, m := range memos {
			l, err := s.st.ListAttachments(ctx, model.AttachmentFilter{MemoID: m.ID})
			if err != nil {
				return nil, err
			}
			out = append(out, l...)
		}
		return out, nil
	}
	notes, err := s.st.ListNotes(ctx, 0, true)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		l, err := s.st.ListAttachments(ctx, model.AttachmentFilter{NoteID: n.ID})
		if err != nil {
			return nil, err
		}
		out = append(out, l...)
	}
	return out, nil
}

func (s *Service) copyAttachment(ctx context.Context, a model.Attachment, w io.Writer) error {
	_, f, err := s.OpenAttachment(ctx, a.ID)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// ExportToDir writes the Markdown export (and uploaded files) to the export directory.
func (s *Service) ExportToDir(ctx context.Context) (int, error) {
	files, err := s.BuildExport(ctx)
	if err != nil {
		return 0, err
	}
	write := func(name string, fn func(io.Writer) error) error {
		p := filepath.Join(s.exportDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		tmp := p + ".tmp"
		f, err := os.Create(tmp) // #nosec -- path built from sanitized segments under exportDir
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, p)
	}
	for name, data := range files {
		if err := write(name, func(w io.Writer) error { _, err := w.Write(data); return err }); err != nil {
			return 0, err
		}
	}
	atts, err := s.exportAttachments(ctx)
	if err != nil {
		return 0, err
	}
	n := len(files)
	for _, ef := range atts {
		err := write(ef.path, func(w io.Writer) error { return s.copyAttachment(ctx, ef.att, w) })
		if errors.Is(err, model.ErrNotFound) {
			continue // file missing on disk: skip
		}
		if err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

// ExportZip streams the export (with uploaded files) as a zip archive.
func (s *Service) ExportZip(ctx context.Context, w io.Writer) error {
	files, err := s.BuildExport(ctx)
	if err != nil {
		return err
	}
	atts, err := s.exportAttachments(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	zw := zip.NewWriter(w)
	for _, n := range names {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: s.Now()})
		if err != nil {
			return err
		}
		if _, err := f.Write(files[n]); err != nil {
			return err
		}
	}
	for _, ef := range atts {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: ef.path, Method: zip.Deflate, Modified: ef.att.UpdatedAt})
		if err != nil {
			return err
		}
		if err := s.copyAttachment(ctx, ef.att, f); err != nil && !errors.Is(err, model.ErrNotFound) {
			return err
		}
	}
	return zw.Close()
}

// MaybeAutoExport runs the daily export once per day after the configured time.
func (s *Service) MaybeAutoExport(ctx context.Context) error {
	set, err := s.Settings(ctx)
	if err != nil || !set.ExportDaily {
		return err
	}
	now := s.Now()
	today := now.Format("2006-01-02")
	if set.LastAutoExport == today {
		return nil
	}
	m, err := schedule.ParseHM(set.ExportTime)
	if err != nil || now.Hour()*60+now.Minute() < m {
		return nil //nolint:nilerr // invalid time: skip
	}
	if _, err := s.ExportToDir(ctx); err != nil {
		return err
	}
	return s.st.SetSetting(ctx, KeyLastAutoExport, today)
}
