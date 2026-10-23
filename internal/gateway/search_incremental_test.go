package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type incrementalSource struct {
	stubMusic
	name   string
	calls  atomic.Int64
	search func(context.Context, provider.SearchRequest) (provider.SearchPage, error)
}

func (s *incrementalSource) ID() string { return s.name }
func (s *incrementalSource) Search(ctx context.Context, _ provider.User, q provider.SearchRequest) (provider.SearchPage, error) {
	s.calls.Add(1)
	return s.search(ctx, q)
}

func incrementalGateway(t *testing.T, handler http.HandlerFunc) *Gateway {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "music.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	g := New(sock)
	t.Cleanup(func() { g.Close(); srv.Close() })
	return g
}

func emptyLocalSearch(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(`{"code":0,"data":{"list":[],"total":0}}`))
}

func TestSearchFetchesOnlyNeededBatchesAndExtendsStablePrefix(t *testing.T) {
	g := incrementalGateway(t, emptyLocalSearch)
	prefs, _ := preferences.New("")
	cfg := prefs.Snapshot()
	cfg.MaxPages = 1
	prefs.Update(cfg)
	g.SetPreferences(prefs)
	sources := []provider.AccountSource{}
	providers := []*incrementalSource{}
	for _, name := range []string{"netease", "qqmusic"} {
		name := name
		s := &incrementalSource{name: name, search: func(_ context.Context, q provider.SearchRequest) (provider.SearchPage, error) {
			if q.Limit != 20 {
				return provider.SearchPage{}, fmt.Errorf("oversized first fetch: %d", q.Limit)
			}
			tracks := []provider.Track{}
			for i := q.Offset; i < q.Offset+q.Limit; i++ {
				id := fmt.Sprint(i + 1)
				if name == "qqmusic" {
					id = fmt.Sprintf("%014d", i+1)
				}
				tracks = append(tracks, provider.Track{ID: id, Source: name, Title: fmt.Sprintf("Hit %d", i), Playable: true})
			}
			return provider.SearchPage{Tracks: tracks, Total: 1000, HasMore: true}, nil
		}}
		providers = append(providers, s)
		sources = append(sources, provider.AccountSource{Adapter: s, Scope: name})
	}
	g.SetAccounts(rankedSourcesResolver{sources: sources})
	request := func(page, size int) ([]json.RawMessage, int) {
		t.Helper()
		r := httptest.NewRequest("GET", fmt.Sprintf("http://localhost/music/api/v1/search/track?q=Hit&page=%d&size=%d", page, size), nil)
		rows, total, err := g.rankedSearch(context.Background(), r, provider.User{FnOSGUID: "alice", Verified: true}, providers[0], "netease", "Hit")
		if err != nil {
			t.Fatal(err)
		}
		return append([]json.RawMessage{}, rows...), total
	}
	first, total := request(1, 50)
	if len(first) != 80 || total != 200 || providers[0].calls.Load() != 2 || providers[1].calls.Load() != 2 {
		t.Fatalf("first page prefetched too much or lost total: rows=%d total=%d calls=%d/%d", len(first), total, providers[0].calls.Load(), providers[1].calls.Load())
	}
	second, _ := request(2, 50)
	if len(second) != 120 || !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatal("later page reordered the prefix")
	}
	request(1, 100)
	if providers[0].calls.Load() != 3 || providers[1].calls.Load() != 3 {
		t.Fatal("page-size change refetched cached rows")
	}
	last, total := request(5, 50)
	if len(last) != 200 || total != 200 || providers[0].calls.Load() != 5 || providers[1].calls.Load() != 5 {
		t.Fatal("candidate budget was not enforced", len(last), total)
	}
	seen := map[string]bool{}
	for _, raw := range last {
		var row struct{ GUID string }
		json.Unmarshal(raw, &row)
		if seen[row.GUID] {
			t.Fatal("duplicate across batches", row.GUID)
		}
		seen[row.GUID] = true
	}
}

func TestSearchStartsLocalAndOnlineBatchesConcurrently(t *testing.T) {
	started := make(chan struct{})
	g := incrementalGateway(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
			emptyLocalSearch(w, r)
		case <-r.Context().Done():
		}
	})
	s := &incrementalSource{name: "netease", search: func(context.Context, provider.SearchRequest) (provider.SearchPage, error) {
		close(started)
		return provider.SearchPage{Tracks: []provider.Track{{ID: "1", Source: "netease", Title: "Hit", Playable: true}}, Total: 1}, nil
	}}
	g.SetAccounts(rankedSourcesResolver{sources: []provider.AccountSource{{Adapter: s, Scope: "ne"}}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=Hit", nil)
	rows, _, err := g.rankedSearch(ctx, r, provider.User{FnOSGUID: "alice", Verified: true}, s, "ne", "Hit")
	if err != nil || len(rows) != 1 {
		t.Fatal("local search blocked online search from starting", len(rows), err)
	}
}

func TestSlowSourceCannotConsumeTheFullSearchTimeout(t *testing.T) {
	g := incrementalGateway(t, emptyLocalSearch)
	slow := &incrementalSource{name: "netease", search: func(ctx context.Context, _ provider.SearchRequest) (provider.SearchPage, error) {
		<-ctx.Done()
		return provider.SearchPage{}, ctx.Err()
	}}
	fast := &groupedTestSource{name: "qqmusic", track: "0039MnYb0qxYhV"}
	g.SetAccounts(rankedSourcesResolver{sources: []provider.AccountSource{{Adapter: slow, Scope: "ne"}, {Adapter: fast, Scope: "qq"}}})
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=Hit", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	rows, _, err := g.rankedSearch(ctx, r, provider.User{FnOSGUID: "alice", Verified: true}, slow, "ne", "Hit")
	if err != nil || len(rows) != 1 || time.Since(start) > 4500*time.Millisecond {
		t.Fatal("fast source waited for the full search timeout", time.Since(start), err)
	}
}

func TestInitialLocalSearchUsesTheRequestDeadlineWithoutProxyRetry(t *testing.T) {
	g := incrementalGateway(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=Hit", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	handled := g.search(w, r, provider.User{FnOSGUID: "alice", Verified: true}, stubMusic{}, "ne", "Hit")
	if !handled || w.Code != http.StatusGatewayTimeout {
		t.Fatal("timed-out request would be retried by the proxy", handled, w.Code)
	}
}

func TestOfficialWebSearchWithoutPaginationIsNotTruncatedAt50(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprintf("interrupted-%t", interrupted), func(t *testing.T) {
			g := incrementalGateway(t, emptyLocalSearch)
			source := &incrementalSource{name: "netease", search: func(_ context.Context, q provider.SearchRequest) (provider.SearchPage, error) {
				if interrupted && q.Offset >= 60 {
					return provider.SearchPage{}, context.DeadlineExceeded
				}
				tracks := []provider.Track{}
				for i := q.Offset; i < min(q.Offset+q.Limit, 120); i++ {
					tracks = append(tracks, provider.Track{ID: fmt.Sprint(i + 1), Source: "netease", Title: fmt.Sprintf("Hit %d", i), Playable: true})
				}
				return provider.SearchPage{Tracks: tracks, Total: 120, HasMore: q.Offset+len(tracks) < 120}, nil
			}}
			request := func(query string) ([]map[string]any, int) {
				t.Helper()
				r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=Hit"+query, nil)
				w := httptest.NewRecorder()
				g.search(w, r, provider.User{FnOSGUID: "alice", Verified: true}, source, "ne", "Hit")
				var body struct {
					Data struct {
						List  []map[string]any
						Total int
					}
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return body.Data.List, body.Data.Total
			}
			first, total := request("&page=1&size=50")
			if len(first) != 50 || total != 120 || source.calls.Load() != 3 {
				t.Fatal("explicit first page was not loaded on demand", len(first), total, source.calls.Load())
			}
			all, total := request("")
			want, calls := 120, int64(6)
			if interrupted {
				want, calls = 60, 4
			}
			if len(all) != want || total != want || source.calls.Load() != calls {
				t.Fatal("unpaged Web response was truncated or advertised inaccessible rows", len(all), total, source.calls.Load())
			}
			if !reflect.DeepEqual(first, all[:50]) {
				t.Fatal("finishing the list reordered existing rows")
			}
			for _, row := range all {
				if !strings.Contains(row["title"].(string), "[网易云]") {
					t.Fatal("source tag lost")
				}
			}
			second, total := request("&page=2&size=50")
			if !reflect.DeepEqual(second, all[50:min(100, len(all))]) || total != want || source.calls.Load() != calls {
				t.Fatal("explicit pagination no longer works after an unpaged request", len(second), total)
			}
		})
	}
}
