// Package provider defines the authenticated source contract for future music providers.
// v0.1 defines these types only; no third-party source integration is present.
package provider

import (
	"context"
	"errors"
	"io"
)

var ErrUnauthenticated = errors.New("fnOS user identity must be verified")
var ErrNotEntitled = errors.New("music content not authorized for this account")

type User struct {
	FnOSGUID string
	Verified bool
}

func (u User) Validate() error {
	if !u.Verified || u.FnOSGUID == "" || u.FnOSGUID == "shared" {
		return ErrUnauthenticated
	}
	return nil
}

type Track struct {
	ID         string   `json:"id"`
	Source     string   `json:"source"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Album      string   `json:"album"`
	DurationMS int64    `json:"duration_ms"`
	CoverURL   string   `json:"cover_url,omitempty"`
	Playable   bool     `json:"playable"`
}
type SearchRequest struct {
	Keyword       string
	Offset, Limit int
}
type SearchPage struct {
	Tracks  []Track
	Total   int
	HasMore bool
}
type PlaybackRequest struct {
	TrackID, Quality string
	Range            string
}
type Stream struct {
	Body                      io.ReadCloser
	StatusCode                int
	ContentType, ContentRange string
	ContentLength             int64
}
type Lyrics struct {
	LRC           string
	TranslatedLRC string
}
type Playlist struct {
	ID, Title, CoverURL string
	Tracks              []Track
	ReadOnly            bool
}

// Adapter deliberately requires a verified fnOS user; provider entitlement is
// checked separately by the authorized source backend. No cookie fallback to a shared account.
type Adapter interface {
	ID() string
	Health(ctx context.Context) error
	Search(ctx context.Context, user User, query SearchRequest) (SearchPage, error)
	GetTrack(ctx context.Context, user User, id string) (Track, error)
	Play(ctx context.Context, user User, req PlaybackRequest) (Stream, error)
	GetLyrics(ctx context.Context, user User, id string) (Lyrics, error)
	ListPlaylists(ctx context.Context, user User) ([]Playlist, error)
}
