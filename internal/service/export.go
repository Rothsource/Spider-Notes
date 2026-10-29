package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	return files, nil
}

// ExportToDir writes the Markdown export to the configured directory.
func (s *Service) ExportToDir(ctx context.Context) (int, error) {
	files, err := s.BuildExport(ctx)
	if err != nil {
		return 0, err
	}
	for name, data := range files {
		p := filepath.Join(s.exportDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return 0, err
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, data, 0o640); err != nil {
			return 0, err
		}
		if err := os.Rename(tmp, p); err != nil {
			return 0, err
		}
	}
	return len(files), nil
}

// ExportZip writes the export as a zip archive.
func (s *Service) ExportZip(ctx context.Context, w io.Writer) error {
	files, err := s.BuildExport(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: s.Now()})
		if err != nil {
			return err
		}
		if _, err := f.Write(files[n]); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
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
