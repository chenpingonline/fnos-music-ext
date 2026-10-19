package gateway

import (
	"errors"
	"net/http"
	"strconv"
)

// Zero (or no size) keeps the original. Bound source requests and cache variants.
func requestedCoverSize(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("size")
	if raw == "" {
		return 0, nil
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size < 0 {
		return 0, errors.New("invalid cover size")
	}
	if size > 2048 {
		size = 2048
	}
	return size, nil
}
func coverVariantKey(key string, size int) string {
	// Version the image cache namespace so old full-resolution entries cannot
	// satisfy a new thumbnail request, even after an application upgrade.
	return key + "\x00image-v2:size=" + strconv.Itoa(size)
}
