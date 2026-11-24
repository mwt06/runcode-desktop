package desktop

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
)

// White-box assertions own their SQL connection instead of exporting the
// production store's writer handle across package boundaries.
func localUsageTestDB(t testing.TB, store *localusage.Store) *sql.DB {
	t.Helper()
	path := filepath.ToSlash(store.Path())
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func localUsageTestCountCalls(t testing.TB, store *localusage.Store, ctx context.Context, scopeID string) (int, error) {
	t.Helper()
	var count int
	err := localUsageTestDB(t, store).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE scope_id=? AND phase='start'`, scopeID).Scan(&count)
	return count, err
}

func localUsageTestCallStatus(t testing.TB, store *localusage.Store, ctx context.Context, callID string) (string, error) {
	t.Helper()
	var status string
	err := localUsageTestDB(t, store).QueryRowContext(ctx,
		`SELECT status FROM events WHERE call_id=? AND phase='finish'`, callID).Scan(&status)
	return status, err
}

func localUsageTestAvailable(store *localusage.Store) bool {
	_, err := store.QueryPage(context.Background(), localusage.QueryFilter{
		ScopeID: "availability-probe", StartAt: time.Unix(1, 0), EndAt: time.Now().Add(time.Hour),
	}, "", 1)
	return err == nil
}
