package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func testSearchTracks(name string, q provider.SearchRequest) provider.SearchPage {
	rows := []provider.Track{}
	for i := q.Offset; i < q.Offset+q.Limit; i++ {
		id := fmt.Sprint(i + 1)
		if name == "qqmusic" {
			id = fmt.Sprintf("%014d", i+1)
		}
		rows = append(rows, provider.Track{ID: id, Source: name, Title: fmt.Sprintf("Hit %03d", i), Playable: true})
	}
	return provider.SearchPage{Tracks: rows, Total: 100, HasMore: q.Offset+q.Limit < 100}
}

func TestQuickSearchParallelPagesAreBoundedAndOrdered(t *testing.T) {
	g := incrementalGateway(t, emptyLocalSearch)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var active, peak atomic.Int64
	perSource := []*atomic.Int64{{}, {}}
	sources := []provider.AccountSource{}
	for i, name := range []string{"netease", "qqmusic"} {
		i, name := i, name
		s := &incrementalSource{name: name, search: func(ctx context.Context, q provider.SearchRequest) (provider.SearchPage, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			own := perSource[i].Add(1)
			defer perSource[i].Add(-1)
			if own > 2 {
				return provider.SearchPage{}, errors.New("per-source concurrency exceeded")
			}
			if q.Offset > 0 {
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return provider.SearchPage{}, ctx.Err()
				}
			}
			// Make lower pages finish later; output must still use original page order.
			if q.Offset == 20 {
				time.Sleep(15 * time.Millisecond)
			}
			return testSearchTracks(name, q), nil
		}}
		sources = append(sources, provider.AccountSource{Adapter: s, Scope: name})
	}
	g.SetAccounts(rankedSourcesResolver{sources: sources})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.search(w, httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=Hit", nil), provider.User{FnOSGUID: "alice", Verified: true}, sources[0].Adapter, "netease", "Hit")
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatal("four page requests did not start concurrently")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("parallel search did not finish")
	}
	if peak.Load() != 4 || active.Load() != 0 {
		t.Fatal("incorrect shared limit", peak.Load(), active.Load())
	}
	var body struct {
		Data struct {
			List  []struct{ GUID string }
			Total int
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if w.Code != 200 || body.Data.Total != 200 {
		t.Fatal(w.Code, body.Data.Total, w.Body.String())
	}
	for i, row := range body.Data.List {
		name, id, ok := fnmusic.DecodeTrackID(row.GUID)
		wantName, wantID := "netease", fmt.Sprint(i/2+1)
		if i%2 == 1 {
			wantName, wantID = "qqmusic", fmt.Sprintf("%014d", i/2+1)
		}
		if !ok || name != wantName || id != wantID {
			t.Fatal("completion order changed ranking", i, name, id)
		}
	}
}

func TestParallelQuickSearchKeepsPagesAfterAFailureAndDeduplicates(t *testing.T) {
	g := incrementalGateway(t, emptyLocalSearch)
	source := &incrementalSource{name: "netease", search: func(ctx context.Context, q provider.SearchRequest) (provider.SearchPage, error) {
		if q.Offset == 20 {
			return provider.SearchPage{}, errors.New("failed second page")
		}
		result := testSearchTracks("netease", q)
		if q.Offset == 40 {
			result.Tracks[0] = testSearchTracks("netease", provider.SearchRequest{Limit: 1}).Tracks[0]
		}
		return result, nil
	}}
	page, err := g.parallelQuickSource(context.Background(), provider.User{FnOSGUID: "alice", Verified: true}, source, "Hit", 100, make(chan struct{}, 4))
	if err == nil || len(page.Tracks) != 79 || page.Total != 79 {
		t.Fatal("lost successful later pages", len(page.Tracks), err)
	}
	if page.Tracks[20].ID != "42" || page.Tracks[len(page.Tracks)-1].ID != "100" {
		t.Fatal("wrong page order or missing later results")
	}
}
