package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// The official Web player loads playlist metadata through batch-detail too.
// Resolve only playlists visible to the authenticated user's current account;
// mixed requests retain their official records and response fields.
func (g *Gateway) playlistBatchDetail(w http.ResponseWriter, r *http.Request) bool {
	user, source, scope, ok := g.playlistSource(r)
	if !ok {
		return false
	}
	raw := r.URL.Query().Get("guids")
	if raw == "" {
		raw = r.URL.Query().Get("guid")
	}
	if len(raw) > 64<<10 {
		http.Error(w, "too many playlists", 400)
		return true
	}
	ids := strings.Split(raw, ",")
	if len(ids) > 1000 {
		http.Error(w, "too many playlists", 400)
		return true
	}
	local, official := []string{}, []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, _, ok := decodeList(scope, strings.TrimPrefix(id, "track_")); ok {
			local = append(local, id)
		} else {
			official = append(official, id)
		}
	}
	if len(local) == 0 {
		return false
	}
	envelope := map[string]json.RawMessage{"code": json.RawMessage(`0`), "msg": json.RawMessage(`"ok"`)}
	data := map[string]json.RawMessage{}
	var existing []json.RawMessage
	if len(official) > 0 {
		req := r.Clone(r.Context())
		u := *r.URL
		q := u.Query()
		q.Del("guid")
		q.Set("guids", strings.Join(official, ","))
		u.RawQuery = q.Encode()
		u.Scheme = "http"
		u.Host = "trim-music"
		req.URL = &u
		req.RequestURI = ""
		resp, err := g.client.Do(req)
		if err != nil {
			http.Error(w, "official playlists unavailable", 502)
			return true
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxOfficialSearchBody+1))
		if err != nil || len(body) > maxOfficialSearchBody {
			http.Error(w, "official playlists unavailable", 502)
			return true
		}
		var code int
		if resp.StatusCode != 200 || json.Unmarshal(body, &envelope) != nil || json.Unmarshal(envelope["code"], &code) != nil || code != 0 {
			writeOfficial(w, resp, body)
			return true
		}
		// Official releases have returned both data.list and a bare data array.
		if json.Unmarshal(envelope["data"], &existing) != nil {
			if json.Unmarshal(envelope["data"], &data) != nil || data == nil || json.Unmarshal(data["list"], &existing) != nil {
				writeOfficial(w, resp, body)
				return true
			}
		}
	}
	if data == nil {
		data = map[string]json.RawMessage{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	lists, err := g.availableLists(ctx, user, source, scope)
	if err != nil {
		diagnostics.Record("playlist-source", scope, err)
		http.Error(w, "playlist source unavailable", 502)
		return true
	}
	cards := make([]json.RawMessage, 0, len(local)+len(existing))
	seen := map[string]bool{}
	for _, guid := range local {
		guid = strings.TrimPrefix(guid, "track_")
		if seen[guid] {
			continue
		}
		seen[guid] = true
		id, kind, _ := decodeList(scope, guid)
		if p, found := hasTypedPlaylist(lists, id, kind); found {
			if p.TrackCount == 0 {
				if count, err := g.listTrackCount(ctx, user, source, scope, p); err == nil {
					p.TrackCount = count
				}
			}

			if card, ok := fnmusic.PlaylistVO(cardScope(scope, p), p); ok {
				encoded, _ := json.Marshal(card)
				cards = append(cards, encoded)
			}
		}
	}
	cards = append(cards, existing...)
	data["list"], _ = json.Marshal(cards)
	envelope["data"], _ = json.Marshal(data)
	writeJSON(w, 200, envelope)
	return true
}

func (g *Gateway) playlistCover(w http.ResponseWriter, r *http.Request) bool {
	guid := r.URL.Query().Get("coverId")
	if guid == "" {
		guid = strings.TrimPrefix(r.URL.Path, playlistBase+"static/cover/")
	}
	guid = strings.TrimPrefix(guid, "track_")
	// Only identify requests shaped like our scoped playlist IDs.
	if len(guid) != 32 {
		return false
	}
	user, source, scope, ok := g.playlistSource(r)
	if !ok {
		return false
	}
	id, kind, ok := decodeList(scope, guid)
	if !ok {
		return false
	}
	size, sizeErr := requestedCoverSize(r)
	if sizeErr != nil {
		http.Error(w, "invalid cover size", 400)
		return true
	}
	lists, err := g.availableLists(r.Context(), user, source, scope)
	if err != nil {
		diagnostics.Record("playlist-source", scope, err)
		http.Error(w, "playlist source unavailable", 502)
		return true
	}
	p, found := hasTypedPlaylist(lists, id, kind)
	if !found {
		http.NotFound(w, r)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	opener, ok := source.(interface {
		OpenPlaylistCover(context.Context, provider.User, provider.Playlist) (provider.Stream, error)
	})
	if !ok {
		http.NotFound(w, r)
		return true
	}
	stage := "playlist-image-fetch"
	fetchCover := func(fetchCtx context.Context) (provider.Stream, error) {
		ctx := fetchCtx
		var img provider.Stream
		seen := map[string]bool{}
		tryImage := func(address string) bool {
			if address == "" || seen[address] || ctx.Err() != nil {
				return false
			}
			seen[address] = true
			candidate := p
			candidate.CoverURL = address
			attempt, cancelAttempt := context.WithTimeout(ctx, 3*time.Second)
			var err error
			if sized, supports := source.(interface {
				OpenPlaylistCoverSized(context.Context, provider.User, provider.Playlist, int) (provider.Stream, error)
			}); supports {
				img, err = sized.OpenPlaylistCoverSized(attempt, user, candidate, size)
			} else {
				img, err = opener.OpenPlaylistCover(attempt, user, candidate)
			}
			cancelAttempt()
			diagnostics.Record("playlist-cover-fetch", scope+":"+guid, err)
			return err == nil
		}
		foundImage := tryImage(p.CoverURL)
		if !foundImage {
			// A non-empty saved URL can expire or fail at its CDN. Fall back to
			// actual track artwork too, rather than only when the URL is absent.
			candidatePage, paged, err := g.listTrackPage(ctx, user, source, scope, p, 0, 10)
			tracks := candidatePage.Tracks
			if !paged {
				tracks, err = g.listTracks(ctx, user, source, scope, p)
			}
			if err != nil {
				stage = "playlist-tracks"
				return provider.Stream{}, err
			}
			for _, track := range tracks {
				if tryImage(track.CoverURL) {
					foundImage = true
					break
				}
				if len(seen) >= 3 {
					break
				}
			}
		}
		if !foundImage {
			return provider.Stream{}, errors.New("playlist cover unavailable")
		}
		return img, nil
	}
	var img provider.Stream
	if g.media != nil {
		img, err = g.media.Cover(ctx, coverVariantKey(user.FnOSGUID+"\x00"+scope+"\x00playlist:"+guid, size), fetchCover)
	} else {
		img, err = fetchCover(ctx)
	}
	if err != nil {
		diagnostics.Record("playlist-cover", scope+":"+guid, err)
		w.Header().Set("X-FnMusic-Cover-Error", stage)
		http.Error(w, "playlist cover unavailable", 502)
		return true
	}
	defer img.Body.Close()
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if img.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(img.ContentLength, 10))
	}
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		io.Copy(w, img.Body)
	}
	return true
}
