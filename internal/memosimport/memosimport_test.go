package memosimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
	"github.com/sovannroth/mynotes/internal/store/sqlite"
)

var png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

// newMemos creates a fake Memos data folder. newer=true uses the recent schema
// (pinned column, attachment table with reference); false uses the older one
// (memo_organizer, resource table with internal_path).
func newMemos(t *testing.T, newer bool) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "memos_prod.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if newer {
		exec(`CREATE TABLE memo (id INTEGER PRIMARY KEY, uid TEXT, creator_id INTEGER, created_ts BIGINT, updated_ts BIGINT, row_status TEXT, content TEXT, visibility TEXT, pinned INTEGER, payload TEXT)`)
		exec(`CREATE TABLE attachment (id INTEGER PRIMARY KEY, uid TEXT, creator_id INTEGER, created_ts BIGINT, updated_ts BIGINT, filename TEXT, blob BLOB, type TEXT, size INTEGER, memo_id INTEGER, storage_type TEXT, reference TEXT, payload TEXT)`)
		exec(`INSERT INTO memo VALUES (1,'aaa',1,1700000000,1700000500,'NORMAL','First memo #life',  'PRIVATE',1,'{}')`)
		exec(`INSERT INTO memo VALUES (2,'bbb',1,1710000000,1710000000,'NORMAL','With pictures',     'PRIVATE',0,'{}')`)
		exec(`INSERT INTO memo VALUES (3,'ccc',1,1720000000,1720000000,'ARCHIVED','Old archived note','PRIVATE',0,'{}')`)
		exec(`INSERT INTO attachment VALUES (1,'f1',1,0,0,'local.png',NULL,'image/png',40,2,'LOCAL','assets/1710000000_local.png','{}')`)
		exec(`INSERT INTO attachment VALUES (2,'f2',1,0,0,'inline.png',?,'image/png',40,2,'DATABASE','','{}')`, png)
		exec(`INSERT INTO attachment VALUES (3,'f3',1,0,0,'cloud.png',NULL,'image/png',40,2,'S3','https://s3/x.png','{}')`)
		if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "assets", "1710000000_local.png"), png, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		exec(`CREATE TABLE memo (id INTEGER PRIMARY KEY, creator_id INTEGER, created_ts BIGINT, updated_ts BIGINT, row_status TEXT, content TEXT, visibility TEXT)`)
		exec(`CREATE TABLE memo_organizer (memo_id INTEGER, user_id INTEGER, pinned INTEGER)`)
		exec(`CREATE TABLE resource (id INTEGER PRIMARY KEY, creator_id INTEGER, created_ts BIGINT, updated_ts BIGINT, filename TEXT, blob BLOB, external_link TEXT, type TEXT, size INTEGER, internal_path TEXT, memo_id INTEGER)`)
		exec(`INSERT INTO memo VALUES (7,1,1690000000,1690000000,'NORMAL','Legacy memo','PRIVATE')`)
		exec(`INSERT INTO memo_organizer VALUES (7,1,1)`)
		exec(`INSERT INTO resource VALUES (1,1,0,0,'doc.txt',?,'','text/plain',5,'',7)`, []byte("hello"))
	}
	return dir
}

func newService(t *testing.T) *service.Service {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "spider.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return service.New(service.Options{Store: db, Location: time.UTC, AttachDir: t.TempDir()})
}

func TestImportNewerSchema(t *testing.T) {
	ctx := context.Background()
	src := newMemos(t, true)
	svc := newService(t)
	rep, err := Run(ctx, svc, src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found != 3 || rep.Imported != 2 || rep.Archived != 1 || rep.FilesCopied != 2 || len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], "S3") {
		t.Fatalf("report %+v", rep)
	}
	memos, _ := svc.ListMemos(ctx, model.MemoFilter{Limit: 10})
	if len(memos) != 2 {
		t.Fatalf("want 2 memos, got %d", len(memos))
	}
	first, pics := memos[1], memos[0] // newest first
	if !first.CreatedAt.Equal(time.Unix(1700000000, 0)) || !first.UpdatedAt.Equal(time.Unix(1700000500, 0)) || !first.Pinned {
		t.Fatalf("dates/pinned not kept: %+v", first)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "life" {
		t.Fatalf("tags %v", first.Tags)
	}
	if strings.Count(pics.ContentMD, "](/attachments/") != 2 {
		t.Fatalf("files not linked: %s", pics.ContentMD)
	}
	if files, _ := svc.ListAttachments(ctx, model.AttachmentFilter{MemoID: pics.ID}); len(files) != 2 {
		t.Fatalf("attachments %d", len(files))
	}

	// Running again never duplicates; --archived adds only the archived one.
	rep, err = Run(ctx, svc, src, Options{IncludeArchived: true})
	if err != nil || rep.Imported != 1 || rep.Skipped != 2 {
		t.Fatalf("rerun %+v %v", rep, err)
	}
	memos, _ = svc.ListMemos(ctx, model.MemoFilter{Limit: 10})
	if len(memos) != 3 || !strings.Contains(memos[0].ContentMD, "#archived") {
		t.Fatalf("archived import: %d", len(memos))
	}
	// The source database was not modified (no -wal/-shm created).
	for _, f := range []string{"memos_prod.db-wal", "memos_prod.db-shm"} {
		if _, err := os.Stat(filepath.Join(src, f)); err == nil {
			t.Fatalf("%s created in source folder", f)
		}
	}
}

func TestImportOlderSchemaAndDryRun(t *testing.T) {
	ctx := context.Background()
	src := newMemos(t, false)
	svc := newService(t)
	rep, err := Run(ctx, svc, src, Options{DryRun: true})
	if err != nil || rep.Imported != 1 {
		t.Fatalf("dry run %+v %v", rep, err)
	}
	if memos, _ := svc.ListMemos(ctx, model.MemoFilter{Limit: 10}); len(memos) != 0 {
		t.Fatal("dry run must not write")
	}
	rep, err = Run(ctx, svc, filepath.Join(src, "memos_prod.db"), Options{}) // path to the .db file also works
	if err != nil || rep.Imported != 1 || rep.FilesCopied != 1 {
		t.Fatalf("import %+v %v", rep, err)
	}
	memos, _ := svc.ListMemos(ctx, model.MemoFilter{Limit: 10})
	if len(memos) != 1 || !memos[0].Pinned || !memos[0].CreatedAt.Equal(time.Unix(1690000000, 0)) {
		t.Fatalf("legacy memo %+v", memos)
	}
	if _, err := Run(ctx, svc, t.TempDir(), Options{}); err == nil {
		t.Fatal("missing database must error")
	}
}
