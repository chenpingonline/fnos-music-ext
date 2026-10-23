package gateway

import (
	"context"
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
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
		if resolver, ok := g.accounts.(interface {
			ResolveUser(provider.User) (provider.Adapter, string, bool)
		}); ok {
			return resolver.ResolveUser(user)
		}
		if scoped, ok := g.accounts.(interface {
			ResolveScoped(string) (provider.Adapter, string, bool)
		}); ok {
			return scoped.ResolveScoped(user.FnOSGUID)
		}
		return g.accounts.Resolve(user.FnOSGUID)
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
	g.listMu.Lock()
	g.listEpoch++
	g.lists = nil
	g.listMu.Unlock()
	g.searchMu.Lock()
	g.searches = nil
	g.rankedSearches = nil
	g.searchMu.Unlock()
	g.recoMu.Lock()
	g.recommendations = nil
	g.recoMu.Unlock()
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

type identityContextKey struct{}
type requestIdentity struct {
	once sync.Once
	user provider.User
	ok   bool
}

func (g *Gateway) identify(ctx context.Context, r *http.Request) (provider.User, bool) {
	if memo, ok := ctx.Value(identityContextKey{}).(*requestIdentity); ok {
		memo.once.Do(func() { memo.user, memo.ok = g.loadIdentity(ctx, r) })
		return memo.user, memo.ok
	}
	return g.loadIdentity(ctx, r)
}
func (g *Gateway) loadIdentity(ctx context.Context, r *http.Request) (provider.User, bool) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://trim-music/music/api/v1/user/me", nil)
	if e != nil {
		return provider.User{}, false
	}
	// Credentials are forwarded ONLY to the local official Unix Socket. They are
	// never sent to the music provider.
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
			GUID     string `json:"guid"`
			Username string `json:"username"`
			UserName string `json:"userName"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&data) != nil || data.Code != 0 {
		return provider.User{}, false
	}
	name := data.Data.Username
	if name == "" {
		name = data.Data.UserName
	}
	if name != "" {
		if local, ok := userdir.Lookup(name); ok {
			if observer, ok := g.accounts.(interface {
				ObserveMusicIdentity(string, string, string) error
			}); ok {
				if err := observer.ObserveMusicIdentity(data.Data.GUID, local.Username, local.UID); err != nil {
					return provider.User{}, false
				}
			}
		}
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
	id, ok := fnmusic.DecodeOnlineID(guid)
	return id, strings.TrimPrefix(guid, "track_"), ok
}
func (g *Gateway) serveOnline(w http.ResponseWriter, r *http.Request) bool {
	if g.playbackRoute(w, r) {
		return true
	}
	if g.downloadRoute(w, r) || g.collectionRoute(w, r) {
		return true
	}
	if g.playlistRoute(w, r) {
		return true
	}
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
		if g.playlistCover(w, r) {
			return true
		}
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
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(g.policy().SearchTimeout)*time.Second)
	defer cancel()
	// Fetch official response first, preserve it byte-for-byte if source fails.
	req := r.Clone(ctx)
	u := *r.URL
	u.Scheme = "http"
	u.Host = "trim-music"
	req.URL = &u
	req.RequestURI = ""
	resp, e := g.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			http.Error(w, "search timed out", http.StatusGatewayTimeout)
			return true
		}
		return false
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxOfficialSearchBody+1))
	if e != nil || len(b) > maxOfficialSearchBody {
		if ctx.Err() != nil {
			http.Error(w, "search timed out", http.StatusGatewayTimeout)
			return true
		}
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
	rows, total, err := g.rankedSearch(ctx, r, user, source, accountID, keyword)
	if err != nil {
		diagnostics.Record("search", accountID, err)
		writeOfficial(w, resp, b)
		return true
	}
	start := (page - 1) * size
	if start > len(rows) {
		start = len(rows)
	}
	end := start + size
	if unpagedSearch(r) {
		end = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	// Preserve official envelope fields while replacing only list and total.
	var envelope map[string]json.RawMessage
	var data map[string]json.RawMessage
	if json.Unmarshal(b, &envelope) != nil || json.Unmarshal(envelope["data"], &data) != nil || data == nil {
		writeOfficial(w, resp, b)
		return true
	}
	data["list"], _ = json.Marshal(rows[start:end])
	data["total"], _ = json.Marshal(total)
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
		if t.Source == "" {
			t.Source = source.ID()
		}
		if t.Source != source.ID() || t.ID != id {
			return provider.Track{}, provider.ErrNotEntitled
		}
	}
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
	source, accountID, allowed := g.trackSource(user, guid)
	if !allowed {
		if _, _, hasAccount := g.sourceFor(user); hasAccount {
			http.Error(w, "track belongs to another source", http.StatusForbidden)
		} else {
			http.Error(w, "no music account selected for this fnOS user", http.StatusForbidden)
		}
		return true
	}
	if !trackSourceMatches(source, guid) {
		http.Error(w, "track belongs to another source", 403)
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
		track = g.resolvePlaybackMetadata(ctx, user, source, accountID, track)
		payload, ok := fnmusic.Metadata(track)
		if !ok {
			http.Error(w, "invalid online track", http.StatusBadGateway)
			return true
		}
		if data, ok := payload["data"].(map[string]any); ok {
			_, favorite := g.favorite(user, accountID, id)
			data["isFavorite"] = favorite
			if nested, ok := data["track"].(map[string]any); ok {
				nested["isFavorite"] = favorite
			}
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
		if g.media != nil {
			if job, ok := g.media.Cached(user.FnOSGUID, accountID, id, g.policy().Quality); ok {
				g.serveJob(w, r, job, false)
				return true
			}
		}
		g.cachePlayback(r, user, accountID, source, id)
		rangeVal := r.Header.Get("Range")
		if r.Method == http.MethodHead {
			rangeVal = "__HEAD__"
		}
		// Audio must use request context rather than a 12s timeout; playback continues
		// while the client is connected and is cancelled on disconnect.
		stream, e := playQuality(r.Context(), source, user, id, g.policy().Quality, rangeVal)
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
		size, sizeErr := requestedCoverSize(r)
		if sizeErr != nil {
			http.Error(w, "invalid cover size", 400)
			return true
		}

		imgSource, ok := source.(interface {
			OpenCover(context.Context, provider.User, string) (provider.Stream, error)
		})
		if !ok {
			http.Error(w, "cover adapter unavailable", 502)
			return true
		}
		fetchCover := func(fetchCtx context.Context) (provider.Stream, error) {
			ctx := fetchCtx
			var img provider.Stream
			var e error
			usedCached := false
			if cached, found := g.cache.get(accountID + ":" + id); found && cached.CoverURL != "" {
				if opener, supports := source.(interface {
					OpenTrackCover(context.Context, provider.User, provider.Track) (provider.Stream, error)
				}); supports {
					// Never accept image URLs from the request. This metadata belongs
					// to the authenticated user's currently selected source scope.
					usedCached = true
					cachedCtx, cancelCached := context.WithTimeout(ctx, 5*time.Second)
					if sized, supports := source.(interface {
						OpenTrackCoverSized(context.Context, provider.User, provider.Track, int) (provider.Stream, error)
					}); supports {
						img, e = sized.OpenTrackCoverSized(cachedCtx, user, cached, size)
					} else {
						img, e = opener.OpenTrackCover(cachedCtx, user, cached)
					}
					cancelCached()
				}
			}
			if !usedCached || e != nil {
				if sized, supports := source.(interface {
					OpenCoverSized(context.Context, provider.User, string, int) (provider.Stream, error)
				}); supports {
					img, e = sized.OpenCoverSized(ctx, user, id, size)
				} else {
					img, e = imgSource.OpenCover(ctx, user, id)
				}
			}
			return img, e
		}
		var img provider.Stream
		var e error
		if g.media != nil {
			img, e = g.media.Cover(ctx, coverVariantKey(user.FnOSGUID+"\x00"+accountID+"\x00track:"+guid, size), fetchCover)
		} else {
			img, e = fetchCover(ctx)
		}
		if e != nil {
			diagnostics.Record("track-cover", accountID+":"+id, e)
			w.Header().Set("X-FnMusic-Cover-Error", diagnostics.Code(e))
			http.Error(w, "cover unavailable", 502)
			return true
		}
		defer img.Body.Close()
		w.Header().Set("Content-Type", img.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
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

func trackSourceMatches(source provider.Adapter, guid string) bool {
	name, _, ok := fnmusic.DecodeTrackID(guid)
	return ok && source != nil && name == source.ID()
}
