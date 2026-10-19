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

type AudioInfo struct {
	Format  string `json:"format"`
	Codec   string `json:"codec"`
	Size    int64  `json:"size"`
	Quality string `json:"quality"`
}
type Track struct {
	Audio        *AudioInfo `json:"audio,omitempty"`
	ID           string     `json:"id"`
	Source       string     `json:"source"`
	Title        string     `json:"title"`
	Artists      []string   `json:"artists"`
	Album        string     `json:"album"`
	DurationMS   int64      `json:"duration_ms"`
	CoverURL     string     `json:"cover_url,omitempty"`
	Playable     bool       `json:"playable"`
	AccessStatus int        `json:"access_status,omitempty"`
}
type TrackPage struct {
	Tracks []Track
	Total  int
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
	IfRange          string
}
type Stream struct {
	Body                      io.ReadCloser
	StatusCode                int
	ContentType, ContentRange string
	ContentLength             int64
	ETag, LastModified        string
}
type Lyrics struct {
	LRC           string
	TranslatedLRC string
}
type Playlist struct {
	Kind       string  `json:"kind,omitempty"`
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	CoverURL   string  `json:"cover_url"`
	Tracks     []Track `json:"tracks,omitempty"`
	TrackCount int     `json:"track_count"`
	ReadOnly   bool    `json:"read_only"`
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
