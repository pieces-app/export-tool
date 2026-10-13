package recovery

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStoreURIFormsAbsoluteFileURIs(t *testing.T) {
	cases := map[string]string{
		"/tmp/pieces work/state.sqlite": "file:///tmp/pieces%20work/state.sqlite?mode=rw",
		// Windows paths start with a drive letter after filepath.ToSlash. The
		// drive must land in the URI path, never in the authority position.
		"C:/Users/a b/AppData/Local/Pieces Export/recovery/s/work/state.sqlite": "file:///C:/Users/a%20b/AppData/Local/Pieces%20Export/recovery/s/work/state.sqlite?mode=rw",
	}
	for in, want := range cases {
		if got := storeURI(in); got != want {
			t.Errorf("storeURI(%q)\n got %s\nwant %s", in, got, want)
		}
	}
}

// SQLite parses "file://C:/..." as a URI with authority "C:" and refuses it
// ("invalid uri authority"). On Windows that left every recovery database at
// zero bytes (issue #10). The URI must be accepted by SQLite's parser on every
// platform; only the missing file may fail the open.
func TestStoreURIAcceptedBySQLiteParser(t *testing.T) {
	for _, in := range []string{"C:/no/such/dir/state.sqlite", "/no/such/dir/state.sqlite"} {
		db, err := sql.Open("sqlite", storeURI(in))
		if err != nil {
			t.Fatal(err)
		}
		err = db.Ping()
		db.Close()
		if err == nil {
			t.Fatalf("%s: expected an open failure for a missing file", in)
		}
		if strings.Contains(err.Error(), "invalid uri authority") {
			t.Fatalf("%s: SQLite rejected the store URI: %v", in, err)
		}
	}
}
