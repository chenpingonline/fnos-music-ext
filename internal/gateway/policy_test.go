package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type policySource struct {
	accountAdapter
	search         func(provider.SearchRequest) (provider.SearchPage, error)
	play           func(provider.PlaybackRequest) (provider.Stream, error)
	recommendCalls int
}

func (s *policySource) Search(_ context.Context, _ provider.User, q provider.SearchRequest) (provider.SearchPage, error) {
	return s.search(q)
}
func (s *policySource) Play(_ context.Context, _ provider.User, q provider.PlaybackRequest) (provider.Stream, error) {
	return s.play(q)
}
func (s *policySource) Recommendations(_ context.Context, u provider.User, kind string) ([]provider.Track, error) {
	s.recommendCalls++
	return []provider.Track{{ID: "1", Title: u.FnOSGUID + kind}}, nil
}
func (s *policySource) PublicPlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error) {
	return []provider.Track{{ID: "2", Title: "public"}}, nil
}
func TestQualityFallbackAndDeepSearchFill(t *testing.T) {
	calls := []string{}
	source := &policySource{play: func(q provider.PlaybackRequest) (provider.Stream, error) {
		calls = append(calls, q.Quality)
		if q.Quality != "exhigh" {
			return provider.Stream{}, errors.New("unavailable")
		}
		return provider.Stream{StatusCode: 200, Body: io.NopCloser(strings.NewReader("audio"))}, nil
	}}
	s, e := playQuality(context.Background(), source, provider.User{FnOSGUID: "alice", Verified: true}, "1", "high", "")
	if e != nil {
		t.Fatal(e)
	}
	s.Body.Close()
	if strings.Join(calls, ",") != "jymaster,hires,lossless,exhigh" {
		t.Fatal(calls)
	}
	pages := 0
	source.search = func(q provider.SearchRequest) (provider.SearchPage, error) {
		pages++
		if q.Offset == 0 {
			return provider.SearchPage{Tracks: []provider.Track{{ID: "1", Title: "blocked", Playable: false}, {ID: "2", Title: "same", Playable: true}, {ID: "3", Title: "same", Playable: true}}, Total: 5, HasMore: true}, nil
		}
		return provider.SearchPage{Tracks: []provider.Track{{ID: "4", Title: "next", Playable: true}, {ID: "5", Title: "last", Playable: true}}, Total: 5}, nil
	}
	g := New("/unused")
	defer g.Close()
	settings, _ := preferences.New("")
	g.SetPreferences(settings)
	u := provider.User{FnOSGUID: "alice", Verified: true}
	result, e := g.searchWindow(context.Background(), u, source, "scope", "keyword", 0, 2)
	if e != nil || len(result.Tracks) != 2 || result.Tracks[1].ID != "4" || pages != 2 {
		t.Fatalf("fill %+v pages=%d %v", result, pages, e)
	}
	cfg := settings.Snapshot()
	cfg.DeepPage = false
	settings.Update(cfg)
	pages = 0
	result, e = g.searchWindow(context.Background(), u, source, "scope", "keyword", 0, 2)
	if e != nil || len(result.Tracks) != 1 || result.Total != 1 || pages != 1 {
		t.Fatalf("deep-page off %+v %v", result, e)
	}
}
func TestRecommendationCacheIsolatedAndRevisionInvalidates(t *testing.T) {
	g := New("/unused")
	defer g.Close()
	settings, _ := preferences.New("")
	g.SetPreferences(settings)
	source := &policySource{}
	playlist := provider.Playlist{ID: "2", Kind: "recommend"}
	for _, user := range []string{"alice", "alice", "bob"} {
		tracks, e := g.listTracks(context.Background(), provider.User{FnOSGUID: user, Verified: true}, source, "same-account", playlist)
		if e != nil || tracks[0].Title != user+"daily" {
			t.Fatal("wrong recommendation owner", tracks, e)
		}
	}
	if source.recommendCalls != 2 {
		t.Fatal("cache missed", source.recommendCalls)
	}
	cfg := settings.Snapshot()
	cfg.Daily = true
	settings.Update(cfg)
	g.listTracks(context.Background(), provider.User{FnOSGUID: "alice", Verified: true}, source, "same-account", playlist)
	if source.recommendCalls != 3 {
		t.Fatal("stale settings cache")
	}
}
func TestProbeAndPageCap(t *testing.T) {
	g := New("/unused")
	defer g.Close()
	settings, _ := preferences.New("")
	cfg := settings.Snapshot()
	cfg.SearchProbe = true
	cfg.MaxPages = 1
	settings.Update(cfg)
	g.SetPreferences(settings)
	searchCalls := 0
	source := &policySource{search: func(q provider.SearchRequest) (provider.SearchPage, error) {
		searchCalls++
		return provider.SearchPage{Tracks: []provider.Track{{ID: "1", Title: "dead", Playable: true}, {ID: "2", Title: "live", Playable: true}}, Total: 1000, HasMore: true}, nil
	}, play: func(r provider.PlaybackRequest) (provider.Stream, error) {
		if r.Range != "__HEAD__" {
			return provider.Stream{}, fmt.Errorf("unexpected media transfer")
		}
		if r.TrackID == "1" {
			return provider.Stream{}, errors.New("dead")
		}
		return provider.Stream{Body: io.NopCloser(strings.NewReader("")), StatusCode: 200}, nil
	}}
	result, e := g.searchWindow(context.Background(), provider.User{FnOSGUID: "alice", Verified: true}, source, "scope", "keyword", 0, 10)
	if e != nil || len(result.Tracks) != 1 || result.Total != 1 || searchCalls != 1 {
		t.Fatalf("probe/cap %+v %v", result, e)
	}
}

func TestAppDownloadQualityProtocol(t *testing.T) {
	for _, row := range []struct {
		config, request string
		original        bool
		want            string
	}{{"app", "", false, "original"}, {"app", "lossless", false, "original"}, {"app", "STANDARD", false, "standard"}, {"app", "mp3", false, "standard"}, {"app", "standard", true, "original"}, {"original", "standard", false, "original"}, {"standard", "lossless", true, "standard"}} {
		if got := downloadMode(row.config, row.request, row.original); got != row.want {
			t.Fatalf("%+v -> %s", row, got)
		}
	}
}

func TestPlaybackPreservesRangeRejection(t *testing.T) {
	calls := 0
	source := &policySource{play: func(provider.PlaybackRequest) (provider.Stream, error) {
		calls++
		return provider.Stream{Body: io.NopCloser(strings.NewReader("")), StatusCode: 416, ContentRange: "bytes */4"}, nil
	}}
	stream, e := playQuality(context.Background(), source, provider.User{FnOSGUID: "alice", Verified: true}, "1", "high", "bytes=8-")
	if e != nil || stream.StatusCode != 416 || calls != 1 {
		t.Fatal("range rejection changed", calls, e)
	}
	stream.Body.Close()
}

type personalPlaylistSource struct {
	policySource
	personalCalls int
}

func (s *personalPlaylistSource) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	s.personalCalls++
	return []provider.Playlist{{ID: "901", Title: "个人歌单"}}, nil
}
func TestPersonalPlaylistsIndependentOfSelectedCharts(t *testing.T) {
	g := New("/unused")
	defer g.Close()
	store, _ := preferences.New("")
	g.SetPreferences(store)
	config := store.Snapshot()
	config.PublicEnabled = true
	config.Public = []preferences.Selection{{ID: "3778678", Title: "热歌榜", Kind: "chart"}}
	store.Update(config)
	source := &personalPlaylistSource{}
	user := provider.User{FnOSGUID: "alice", Verified: true}
	lists, err := g.availableLists(context.Background(), user, source, "scope")
	if err != nil || len(lists) != 2 || source.personalCalls != 1 {
		t.Fatal(lists, err)
	}
	config.PersonalPlaylists = false
	store.Update(config)
	lists, err = g.availableLists(context.Background(), user, source, "scope")
	if err != nil || len(lists) != 1 || lists[0].Kind != "public" || lists[0].Title != "热歌榜" || source.personalCalls != 1 {
		t.Fatal("personal switch affected charts or fetched personal lists", lists, err)
	}
	if _, found := hasTypedPlaylist(lists, "901", ""); found {
		t.Fatal("disabled personal playlist remains accessible")
	}
	config.PersonalPlaylists = true
	store.Update(config)
	lists, err = g.availableLists(context.Background(), user, source, "scope")
	if err != nil || len(lists) != 2 || source.personalCalls != 2 {
		t.Fatal("enable did not restore personal lists", lists, err)
	}
}

func TestPersonalSelectionsAreScopedAndRevisionAware(t *testing.T) {
	g := New("/unused")
	defer g.Close()
	store, _ := preferences.New("")
	g.SetPreferences(store)
	cfg := store.Snapshot()
	source := &personalPlaylistSource{}
	user := provider.User{FnOSGUID: "user", Verified: true}
	load := func(scope string) []provider.Playlist {
		t.Helper()
		rows, err := g.availableLists(context.Background(), user, source, scope)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if len(load("alice:session")) != 1 {
		t.Fatal("legacy default hides personal playlists")
	}
	cfg.PersonalSelections = map[string][]string{"alice": {}}
	if err := store.Update(cfg); err != nil {
		t.Fatal(err)
	}
	if len(load("alice:session")) != 0 {
		t.Fatal("empty selection did not hide playlists or cache was stale")
	}
	if len(load("bob:session")) != 1 {
		t.Fatal("selection leaked to another account")
	}
	cfg.PersonalSelections["alice"] = []string{"901", "not-owned"}
	store.Update(cfg)
	rows := load("alice:session")
	if len(rows) != 1 || rows[0].ID != "901" {
		t.Fatal("selection bypassed ownership", rows)
	}
	cfg.PersonalSelections["alice"] = []string{"not-owned"}
	store.Update(cfg)
	if len(load("alice:session")) != 0 {
		t.Fatal("unselected personal playlist remains available")
	}
	cfg.Hot = true
	store.Update(cfg)
	rows = load("alice:session")
	if len(rows) != 1 || rows[0].Kind != "recommend" {
		t.Fatal("personal selection affected recommendations", rows)
	}
}
