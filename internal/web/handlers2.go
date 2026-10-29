package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// ---- calendar ----

type calDay struct {
	Date    time.Time
	ISO     string
	InMonth bool
	Today   bool
	Items   []service.CalItem
	More    int
	Memos   int
}

type hourRow struct {
	Hour  int
	Cells [7][]service.CalItem
}

const maxItemsPerCell = 4

func (s *Server) calendarPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := s.svc.Now()
	anchor := now
	if d, err := time.ParseInLocation("2006-01-02", q.Get("date"), s.loc); err == nil {
		anchor = d
	}
	view := q.Get("view")
	if view != "week" {
		view = "month"
	}
	anchor = s.svc.StartOfDay(anchor)
	data := page{"Title": "Calendar", "View": view, "Anchor": anchor}
	todayISO := now.Format("2006-01-02")

	var from, to time.Time
	if view == "month" {
		first := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, s.loc)
		from = first.AddDate(0, 0, -((int(first.Weekday()) + 6) % 7))
		to = from.AddDate(0, 0, 42)
		data["Label"] = first.Format("January 2006")
		data["Prev"] = first.AddDate(0, -1, 0).Format("2006-01-02")
		data["Next"] = first.AddDate(0, 1, 0).Format("2006-01-02")
	} else {
		from = anchor.AddDate(0, 0, -((int(anchor.Weekday()) + 6) % 7))
		to = from.AddDate(0, 0, 7)
		data["Label"] = from.Format("02 Jan") + " – " + to.AddDate(0, 0, -1).Format("02 Jan 2006")
		data["Prev"] = from.AddDate(0, 0, -7).Format("2006-01-02")
		data["Next"] = from.AddDate(0, 0, 7).Format("2006-01-02")
	}
	cal, err := s.svc.Calendar(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byDay := map[string][]service.CalItem{}
	for _, it := range cal.Items {
		k := it.At.In(s.loc).Format("2006-01-02")
		byDay[k] = append(byDay[k], it)
	}
	var days []calDay
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		iso := d.Format("2006-01-02")
		cd := calDay{Date: d, ISO: iso, InMonth: d.Month() == anchor.Month() || view == "week", Today: iso == todayISO, Memos: cal.MemoDays[iso]}
		items := byDay[iso]
		if view == "month" && len(items) > maxItemsPerCell {
			cd.More = len(items) - maxItemsPerCell
			items = items[:maxItemsPerCell]
		}
		cd.Items = items
		days = append(days, cd)
	}
	if view == "month" {
		var weeks [][]calDay
		for i := 0; i < len(days); i += 7 {
			weeks = append(weeks, days[i:i+7])
		}
		// Drop a trailing week entirely outside the month.
		if last := weeks[len(weeks)-1]; !last[0].InMonth {
			weeks = weeks[:len(weeks)-1]
		}
		data["Weeks"] = weeks
	} else {
		rows := make([]hourRow, 24)
		for h := range rows {
			rows[h].Hour = h
		}
		for i, d := range days {
			for _, it := range byDay[d.ISO] {
				h := it.At.In(s.loc).Hour()
				rows[h].Cells[i] = append(rows[h].Cells[i], it)
			}
		}
		data["Days"] = days
		data["Rows"] = rows
	}
	data["Today"] = todayISO
	s.render(w, r, "calendar", data)
}

func (s *Server) calendarDay(w http.ResponseWriter, r *http.Request) {
	d, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), s.loc)
	if err != nil {
		s.fail(w, r, model.Invalid("bad date"))
		return
	}
	detail, err := s.svc.Day(r.Context(), d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.partial(w, r, "daypanel", detail)
}

// ---- memos ----

type memoGroup struct {
	Day   time.Time
	ISO   string
	Memos []model.Memo
}

type memoPage struct {
	Groups   []memoGroup
	MoreURL  string
	LastDay  string
	ShowHead bool
}

const memoPageSize = 30

func (s *Server) memoPageData(r *http.Request) (memoPage, error) {
	q := r.URL.Query()
	f := model.MemoFilter{Tag: q.Get("tag"), Query: q.Get("q"), Limit: memoPageSize}
	if d := q.Get("date"); d != "" {
		if day, err := time.ParseInLocation("2006-01-02", d, s.loc); err == nil {
			end := day.AddDate(0, 0, 1)
			f.From, f.To = &day, &end
		}
	}
	if b, _ := strconv.ParseInt(q.Get("before"), 10, 64); b > 0 {
		m, err := s.svc.GetMemo(r.Context(), b)
		if err != nil {
			return memoPage{}, err
		}
		f.Before, f.BeforeID = &m.CreatedAt, m.ID
	}
	memos, err := s.svc.ListMemos(r.Context(), f)
	if err != nil {
		return memoPage{}, err
	}
	mp := memoPage{}
	prevDay := q.Get("day")
	for _, m := range memos {
		iso := m.CreatedAt.In(s.loc).Format("2006-01-02")
		if len(mp.Groups) == 0 || mp.Groups[len(mp.Groups)-1].ISO != iso {
			mp.Groups = append(mp.Groups, memoGroup{Day: s.svc.StartOfDay(m.CreatedAt), ISO: iso})
		}
		g := &mp.Groups[len(mp.Groups)-1]
		g.Memos = append(g.Memos, m)
	}
	if len(mp.Groups) > 0 && mp.Groups[0].ISO == prevDay {
		mp.Groups[0].ISO = "" // continuation: suppress repeated header
	}
	if len(memos) == memoPageSize {
		last := memos[len(memos)-1]
		v := url.Values{}
		v.Set("before", strconv.FormatInt(last.ID, 10))
		v.Set("day", last.CreatedAt.In(s.loc).Format("2006-01-02"))
		for _, k := range []string{"tag", "q", "date"} {
			if q.Get(k) != "" {
				v.Set(k, q.Get(k))
			}
		}
		mp.MoreURL = "/memos/more?" + v.Encode()
	}
	return mp, nil
}

func (s *Server) memosPage(w http.ResponseWriter, r *http.Request) {
	mp, err := s.memoPageData(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tags, _ := s.svc.TagCounts(r.Context(), "memo")
	var pinned []model.Memo
	q := r.URL.Query()
	if q.Get("tag") == "" && q.Get("q") == "" && q.Get("date") == "" {
		pinned, _ = s.svc.PinnedMemos(r.Context())
	}
	s.render(w, r, "memos", page{"Title": "Memos", "Page": mp, "Tags": tags, "Pinned": pinned,
		"Tag": q.Get("tag"), "Q": q.Get("q"), "Date": q.Get("date")})
}

func (s *Server) memosMore(w http.ResponseWriter, r *http.Request) {
	mp, err := s.memoPageData(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.partial(w, r, "memopage", mp)
}

func (s *Server) memoCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	m, err := s.svc.CreateMemo(r.Context(), r.FormValue("content"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if isHX(r) {
		s.partial(w, r, "memo", m)
		return
	}
	redirect(w, r, "/memos", "")
}

func (s *Server) memoCard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		var m *model.Memo
		if m, err = s.svc.GetMemo(r.Context(), id); err == nil {
			s.partial(w, r, "memo", m)
			return
		}
	}
	s.fail(w, r, err)
}

func (s *Server) memoEdit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		var m *model.Memo
		if m, err = s.svc.GetMemo(r.Context(), id); err == nil {
			s.partial(w, r, "memoedit", m)
			return
		}
	}
	s.fail(w, r, err)
}

func (s *Server) memoUpdate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.svc.UpdateMemo(r.Context(), id, r.FormValue("content"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if isHX(r) {
		s.partial(w, r, "memo", m)
		return
	}
	redirect(w, r, "/memos", "")
}

func (s *Server) memoAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var m *model.Memo
	switch r.PathValue("action") {
	case "pin":
		m, err = s.svc.SetMemoPinned(ctx, id, true)
	case "unpin":
		m, err = s.svc.SetMemoPinned(ctx, id, false)
	case "toggle":
		var idx int
		if idx, err = strconv.Atoi(r.URL.Query().Get("idx")); err != nil {
			err = model.Invalid("bad checkbox index")
		} else {
			m, err = s.svc.ToggleMemoCheckbox(ctx, id, idx)
		}
	case "delete":
		if err = s.svc.DeleteMemo(ctx, id); err == nil {
			if isHX(r) {
				w.WriteHeader(http.StatusOK)
				return
			}
			redirect(w, r, "/memos", "Memo deleted")
			return
		}
	default:
		err = model.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if isHX(r) {
		s.partial(w, r, "memo", m)
		return
	}
	redirect(w, r, "/memos", "")
}

// ---- history ----

type histGroup struct {
	Day   time.Time
	Items []model.Occurrence
}

func (s *Server) historyPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := s.svc.Now()
	anchor := s.svc.StartOfDay(now)
	if d, err := time.ParseInLocation("2006-01-02", q.Get("date"), s.loc); err == nil {
		anchor = d
	}
	rng := q.Get("range")
	var from, to time.Time
	switch rng {
	case "day":
		from, to = anchor, anchor.AddDate(0, 0, 1)
	case "month":
		from = time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, s.loc)
		to = from.AddDate(0, 1, 0)
	default:
		rng = "week"
		from = anchor.AddDate(0, 0, -((int(anchor.Weekday()) + 6) % 7))
		to = from.AddDate(0, 0, 7)
	}
	f := service.HistoryFilter{From: from, To: to, Kind: q.Get("kind"), Tag: q.Get("tag"), Status: q.Get("status")}
	occs, err := s.svc.History(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var groups []histGroup
	counts := map[string]int{}
	for _, o := range occs {
		counts[o.Status]++
		d := s.svc.StartOfDay(service.OutcomeTime(&o))
		if len(groups) == 0 || !groups[len(groups)-1].Day.Equal(d) {
			groups = append(groups, histGroup{Day: d})
		}
		groups[len(groups)-1].Items = append(groups[len(groups)-1].Items, o)
	}
	stats, _ := s.svc.Stats(r.Context())
	tags, _ := s.svc.TagCounts(r.Context(), "")
	prev, next := from.AddDate(0, 0, -1), to
	if rng == "month" {
		prev = from.AddDate(0, -1, 0)
	} else if rng == "week" {
		prev = from.AddDate(0, 0, -7)
	}
	s.render(w, r, "history", page{"Title": "History", "Groups": groups, "Counts": counts, "Stats": stats, "Tags": tags,
		"Range": rng, "From": from, "To": to.AddDate(0, 0, -1), "F": f, "Prev": prev.Format("2006-01-02"), "Next": next.Format("2006-01-02"),
		"Total": len(occs)})
}

// ---- search ----

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []model.SearchHit
	var err error
	if q != "" {
		if hits, err = s.svc.Search(r.Context(), q, 100); err != nil && !errors.Is(err, model.ErrValidation) {
			s.fail(w, r, err)
			return
		}
	}
	s.render(w, r, "search", page{"Title": "Search", "Q": q, "Hits": hits})
}

// ---- settings ----

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, page{})
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, data page) {
	set, err := s.svc.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tokens, _ := s.auth.ListTokens(r.Context())
	data["Title"] = "Settings"
	data["S"] = set
	data["Tokens"] = tokens
	data["TokenSet"] = set.TelegramToken != ""
	if set.TelegramToken != "" {
		if i := strings.Index(set.TelegramToken, ":"); i > 0 {
			data["TokenHint"] = set.TelegramToken[:i] + ":••••••"
		}
	}
	s.render(w, r, "settings", data)
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	ctx := r.Context()
	var err error
	msg := "Saved"
	switch r.PathValue("section") {
	case "password":
		if r.FormValue("new") != r.FormValue("confirm") {
			err = model.Invalid("New passwords do not match")
		} else {
			err = s.auth.ChangePassword(ctx, r.FormValue("current"), r.FormValue("new"))
			msg = "Password changed. Other sessions were logged out."
		}
	case "general":
		err = s.svc.UpdateSettings(ctx, service.SettingsInput{
			QuietHours:    joinRange(r.FormValue("quiet_start"), r.FormValue("quiet_end")),
			SnoozeMorning: r.FormValue("snooze_morning"), DefaultNagInterval: formInt(r, "nag_default"),
			ExportDaily: r.FormValue("export_daily") == "on", ExportTime: r.FormValue("export_time")})
	case "telegram":
		tok := r.FormValue("token")
		if tok == "" && r.FormValue("clear") != "on" {
			tok = "-" // keep the stored token
		}
		err = s.svc.UpdateTelegram(ctx, tok, r.FormValue("chat_id"))
		msg = "Telegram settings saved"
	case "telegram-test":
		if s.tg == nil {
			err = model.Invalid("Telegram is not available")
		} else if e := s.tg.SendTest(ctx); e != nil {
			err = model.Invalid("Test failed: " + e.Error())
		}
		msg = "Test message sent. Check Telegram."
	case "export":
		var n int
		if n, err = s.svc.ExportToDir(ctx); err == nil {
			msg = "Exported " + strconv.Itoa(n) + " files to the export directory"
		}
	case "tokens":
		plain, _, e := s.auth.CreateToken(ctx, r.FormValue("name"))
		if e != nil {
			err = e
			break
		}
		s.renderSettings(w, r, page{"NewToken": plain, "Msg": "Token created. Copy it now: it will not be shown again."})
		return
	default:
		err = model.ErrNotFound
	}
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			w.WriteHeader(http.StatusBadRequest)
			s.renderSettings(w, r, page{"Error": ve.Msg})
			return
		}
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/settings", msg)
}

func joinRange(a, b string) string {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return ""
	}
	return a + "-" + b
}

func (s *Server) exportZip(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="mynotes-export-`+s.svc.Now().Format("2006-01-02")+`.zip"`)
	if err := s.svc.ExportZip(r.Context(), w); err != nil {
		s.fail(w, r, err)
	}
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = s.auth.RevokeToken(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/settings", "Token revoked")
}
