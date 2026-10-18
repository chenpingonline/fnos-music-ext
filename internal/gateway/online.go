package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

const maxOfficialSearchBody = 6 << 20

// AccountResolver is implemented by the persistent accounts.Manager. A verified
// fnOS identity is ALWAYS required, regardless of the configured isolation mode.
type AccountResolver interface {
	Resolve(fnOSGUID string) (provider.Adapter, string, bool)
	IsConfigured() bool
}

func (g *Gateway) SetAccounts(manager AccountResolver) { g.accounts = manager }
func (g *Gateway) sourceFor(user provider.User) (provider.Adapter, string, bool) {
	if user.Validate() != nil {
		return nil, "", false
	}
	if g.accounts != nil {
		a, id, ok := g.accounts.Resolve(user.FnOSGUID)
		if namespace, yes := g.accounts.(interface{ CacheNamespace(string) string }); yes {
			id = namespace.CacheNamespace(id)
		}
		return a, id, ok
	}
	if g.online != nil && user.FnOSGUID == g.ownerGUID {
		return g.online, "legacy", true
	}
	return nil, "", false
}

func (g *Gateway) SetOnline(source provider.Adapter, ownerGUID string) {
	if source == nil || strings.TrimSpace(ownerGUID) == "" || ownerGUID == "shared" {
		return
	}
	g.online = source
	g.ownerGUID = ownerGUID
}

type cachedTrack struct {
	track provider.Track
	until time.Time
}
type trackCache struct {
	mu      sync.Mutex
	entries map[string]cachedTrack
}

func newTrackCache() *trackCache { return &trackCache{entries: make(map[string]cachedTrack)} }
func (c *trackCache) put(key string, t provider.Track) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) > 500 {
		for k := range c.entries {
			delete(c.entries, k)
			if len(c.entries) < 250 {
				break
			}
		}
	}
	c.entries[key] = cachedTrack{t, time.Now().Add(20 * time.Minute)}
}
func (c *trackCache) get(id string) (provider.Track, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[id]
	if ok && time.Now().Before(v.until) {
		return v.track, true
	}
	delete(c.entries, id)
	return provider.Track{}, false
}

func (g *Gateway) identify(ctx context.Context, r *http.Request) (provider.User, bool) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://trim-music/music/api/v1/user/me", nil)
	if e != nil {
		return provider.User{}, false
	}
	// Credentials are forwarded ONLY to the local official Unix Socket. They are
	// never sent to the MusicBox bridge.
	for _, k := range []string{"Cookie", "Authorization", "X-Trim-Music-Temp-Token"} {
		if val := r.Header.Values(k); len(val) > 0 {
			req.Header[k] = append([]string(nil), val...)
		}
	}
	resp, e := g.client.Do(req)
	if e != nil {
		return provider.User{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return provider.User{}, false
	}
	var data struct {
		Code int `json:"code"`
		Data struct {
			GUID string `json:"guid"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&data) != nil || data.Code != 0 {
		return provider.User{}, false
	}
	user := provider.User{FnOSGUID: data.Data.GUID, Verified: true}
	return user, user.Validate() == nil
}
func onlineGUID(r *http.Request) (string, string, bool) {
	q := r.URL.Query()
	guid := ""
	for _, k := range []string{"guid", "trackGUID", "trackGuid", "coverId", "id", "trackId"} {
		if v := q.Get(k); v != "" {
			guid = v
			break
		}
	}
	if guid == "" {
		segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(segments) > 0 {
			guid = segments[len(segments)-1]
		}
	}
	id, ok := fnmusic.DecodeNetEaseID(guid)
	return id, strings.TrimPrefix(guid, "track_"), ok
}
func (g *Gateway) serveOnline(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/music/api/v1/search/track") && r.Method == http.MethodGet:
		kw := strings.TrimSpace(r.URL.Query().Get("q"))
		if kw == "" {
			kw = strings.TrimSpace(r.URL.Query().Get("keyword"))
		}
		if kw == "" {
			kw = strings.TrimSpace(r.URL.Query().Get("query"))
		}
		if kw == "" {
			return false
		}
		user, ok := g.identify(r.Context(), r)
		if !ok {
			return false
		}
		source, accountID, allowed := g.sourceFor(user)
		if !allowed {
			return false
		}
		return g.search(w, r, user, source, accountID, kw)
	case (path == "/music/api/v1/track/stream" || strings.HasPrefix(path, "/music/api/v1/track/stream/")) && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		return g.onlineSong(w, r, "stream")
	case (path == "/music/api/v1/track/metadata" || strings.HasPrefix(path, "/music/api/v1/track/metadata/") || path == "/music/api/v1/track/audio-info") && r.Method == http.MethodGet:
		return g.onlineSong(w, r, "metadata")
	case (path == "/music/api/v1/lyric/list" || strings.HasPrefix(path, "/music/api/v1/lyric/list/")) && r.Method == http.MethodGet:
		return g.onlineSong(w, r, "lyric-list")
	case (path == "/music/api/v1/track/lyrics" || strings.HasPrefix(path, "/music/api/v1/track/lyrics/") || strings.HasPrefix(path, "/music/api/v1/detail/lyrics/")) && r.Method == http.MethodGet:
		return g.onlineSong(w, r, "lyrics")
	case (path == "/music/api/v1/static/cover" || strings.HasPrefix(path, "/music/api/v1/static/cover/")) && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		return g.onlineSong(w, r, "cover")
	}
	return false
}

func (g *Gateway) search(w http.ResponseWriter, r *http.Request, user provider.User, source provider.Adapter, accountID string, keyword string) bool {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 200 {
		return false
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 {
		size = 50
	}
	if size > 100 {
		return false
	}
	if len([]rune(keyword)) > 120 {
		return false
	}
	// Fetch official response first, preserve it byte-for-byte if source fails.
	req := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = "http"
	u.Host = "trim-music"
	req.URL = &u
	req.RequestURI = ""
	resp, e := g.client.Do(req)
	if e != nil {
		return false
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxOfficialSearchBody+1))
	if e != nil || len(b) > maxOfficialSearchBody {
		return false
	}
	if resp.StatusCode != 200 {
		writeOfficial(w, resp, b)
		return true
	}
	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			List  []json.RawMessage `json:"list"`
			Total int               `json:"total"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &body) != nil || body.Code != 0 || body.Data.Total < 0 {
		writeOfficial(w, resp, b)
		return true
	}
	localTotal := body.Data.Total
	start := (page - 1) * size
	end := start + size
	localPage := body.Data.List
	if start >= localTotal {
		localPage = nil
	} else if len(localPage) > size { // official server sometimes ignores page/size
		lo := start
		if lo > len(localPage) {
			lo = len(localPage)
		}
		hi := end
		if hi > len(localPage) {
			hi = len(localPage)
		}
		localPage = localPage[lo:hi]
	}
	if len(localPage) > size {
		localPage = localPage[:size]
	}
	onlineStart := start - localTotal
	if onlineStart < 0 {
		onlineStart = 0
	}
	remaining := size - len(localPage)
	if remaining < 0 {
		remaining = 0
	}
	// The search page must be stable even if the provider is slow or unavailable.
	// Keep the first response local; online errors never alter official semantics.
	var items []json.RawMessage
	onlineTotal := 0
	if remaining >= 0 {
		fetchLimit := remaining
		if fetchLimit == 0 {
			fetchLimit = 1
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		result, e := source.Search(ctx, user, provider.SearchRequest{Keyword: keyword, Offset: onlineStart, Limit: fetchLimit})
		cancel()
		if e != nil {
			log.Printf("fnmusic-go: music source search unavailable: %T", e)
			writeOfficial(w, resp, b)
			return true
		}
		onlineTotal = result.Total
		seen := make(map[string]bool)
		for _, local := range localPage {
			var m struct {
				Title  string `json:"title"`
				Artist string `json:"artist"`
			}
			_ = json.Unmarshal(local, &m)
			seen[strings.ToLower(m.Title)+"\x00"+strings.ToLower(m.Artist)] = true
		}
		for _, t := range result.Tracks {
			if remaining == 0 {
				break
			}
			if !t.Playable {
				continue
			}
			key := strings.ToLower(t.Title) + "\x00" + strings.ToLower(strings.Join(t.Artists, " "))
			if seen[key] {
				continue
			}
			seen[key] = true
			vo, valid := fnmusic.TrackVO(t, true)
			if !valid {
				continue
			}
			raw, e := json.Marshal(vo)
			if e != nil {
				continue
			}
			items = append(items, raw)
			g.cache.put(accountID+":"+t.ID, t)
		}
	}
	if len(items) == 0 && start < localTotal { // Still publish online total to enable subsequent pages.
	}
	// Preserve unrelated fields in the official JSON by editing only list/total.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(b, &envelope) != nil {
		writeOfficial(w, resp, b)
		return true
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(envelope["data"], &data) != nil {
		writeOfficial(w, resp, b)
		return true
	}
	merged := append(append(make([]json.RawMessage, 0, len(localPage)+len(items)), localPage...), items...)
	raw, _ := json.Marshal(merged)
	data["list"] = raw
	if onlineTotal < onlineStart+len(items) {
		onlineTotal = onlineStart + len(items)
	}
	count, _ := json.Marshal(localTotal + onlineTotal)
	data["total"] = count
	envelope["data"], _ = json.Marshal(data)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(envelope)
	return true
}
func writeOfficial(w http.ResponseWriter, resp *http.Response, b []byte) {
	for _, k := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}
func (g *Gateway) getTrack(ctx context.Context, user provider.User, source provider.Adapter, accountID, id string) (provider.Track, error) {
	if t, ok := g.cache.get(accountID + ":" + id); ok {
		return t, nil
	}
	t, e := source.GetTrack(ctx, user, id)
	if e == nil {
		g.cache.put(accountID+":"+id, t)
	}
	return t, e
}
func (g *Gateway) onlineSong(w http.ResponseWriter, r *http.Request, action string) bool {
	id, guid, ok := onlineGUID(r)
	if !ok {
		return false
	}
	user, authed := g.identify(r.Context(), r)
	if !authed {
		http.Error(w, "verified fnOS account required", http.StatusForbidden)
		return true
	}
	source, accountID, allowed := g.sourceFor(user)
	if !allowed {
		http.Error(w, "no music account selected for this fnOS user", http.StatusForbidden)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	switch action {
	case "metadata":
		track, e := g.getTrack(ctx, user, source, accountID, id)
		if e != nil {
			http.Error(w, "online metadata unavailable", http.StatusBadGateway)
			return true
		}
		payload, ok := fnmusic.Metadata(track)
		if !ok {
			http.Error(w, "invalid online track", http.StatusBadGateway)
			return true
		}
		writeJSON(w, 200, payload)
	case "lyrics", "lyric-list":
		lyric, e := source.GetLyrics(ctx, user, id)
		if e != nil {
			http.Error(w, "lyrics unavailable", http.StatusBadGateway)
			return true
		}
		writeJSON(w, 200, fnmusic.Lyrics(guid, lyric, action == "lyric-list"))
	case "stream":
		rangeVal := r.Header.Get("Range")
		if r.Method == http.MethodHead {
			rangeVal = "__HEAD__"
		}
		// Audio must use request context rather than a 12s timeout; playback continues
		// while the client is connected and is cancelled on disconnect.
		stream, e := source.Play(r.Context(), user, provider.PlaybackRequest{TrackID: id, Quality: "exhigh", Range: rangeVal})
		if e != nil {
			http.Error(w, "online audio unavailable or not entitled", http.StatusBadGateway)
			return true
		}
		defer stream.Body.Close()
		w.Header().Set("Content-Type", stream.ContentType)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		if stream.ContentRange != "" {
			w.Header().Set("Content-Range", stream.ContentRange)
		}
		if stream.ContentLength >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(stream.ContentLength, 10))
		}
		w.WriteHeader(stream.StatusCode)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, stream.Body)
		}
	case "cover":
		imgSource, ok := source.(interface {
			OpenCover(context.Context, provider.User, string) (provider.Stream, error)
		})
		if !ok {
			http.Error(w, "cover adapter unavailable", 502)
			return true
		}
		img, e := imgSource.OpenCover(ctx, user, id)
		if e != nil {
			http.Error(w, "cover unavailable", 502)
			return true
		}
		defer img.Body.Close()
		w.Header().Set("Content-Type", img.ContentType)
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("Content-Length", strconv.FormatInt(img.ContentLength, 10))
		w.WriteHeader(200)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, img.Body)
		}
	}
	return true
}

// for tests and for diagnostic status.
func (g *Gateway) OnlineEnabled() bool {
	if g.accounts != nil {
		return g.accounts.IsConfigured()
	}
	return g.online != nil && g.ownerGUID != ""
}
func (g *Gateway) CurrentOwner() string { return g.ownerGUID }
func (g *Gateway) SearchURL(query string) string {
	return (&url.URL{Path: "/music/api/v1/search/track", RawQuery: url.Values{"q": {query}}.Encode()}).String()
}
