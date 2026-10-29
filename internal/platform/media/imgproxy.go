package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

// Preset is a delivery size: images are fitted into a Size × Size box
// and never enlarged. A fixed set keeps the CDN cache small.
type Preset struct {
	Name string
	Size int
}

// Presets are the delivery sizes, smallest first.
func Presets() []Preset {
	return []Preset{{"thumb", 160}, {"small", 400}, {"medium", 800}, {"large", 1600}}
}

// Formats are the delivery formats; the storefront offers both through
// <picture>, so the CDN never varies on Accept.
func Formats() []string { return []string{"avif", "webp"} }

// Product feeds' images (ADR-0044): the largest preset as a JPEG, the one
// format every ad platform's catalog takes.
const (
	FeedFormat = "jpg"
	FeedSize   = 1600
)

// Signer builds signed imgproxy URLs for masters in one bucket. imgproxy
// refuses unsigned or altered URLs, so nobody can make it resize
// arbitrary sources or sizes.
type Signer struct {
	base   string // e.g. https://img.iftahonline.com (no trailing slash)
	bucket string
	key    []byte
	salt   []byte
}

// NewSigner returns a signer. key and salt are the decoded IMGPROXY_KEY
// and IMGPROXY_SALT.
func NewSigner(baseURL, bucket string, key, salt []byte) *Signer {
	return &Signer{base: strings.TrimRight(baseURL, "/"), bucket: bucket, key: key, salt: salt}
}

// URL returns the signed URL of objectKey fitted into size × size, in
// format (avif or webp).
func (s *Signer) URL(objectKey string, size int, format string) string {
	src := base64.RawURLEncoding.EncodeToString([]byte("s3://" + s.bucket + "/" + objectKey))
	n := strconv.Itoa(size)
	path := "/rs:fit:" + n + ":" + n + ":0/" + src + "." + format
	return s.base + "/" + s.sign(path) + path
}

// sign is imgproxy's scheme: base64url(HMAC-SHA256(key, salt || path)).
func (s *Signer) sign(path string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(s.salt)
	mac.Write([]byte(path))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// URLs returns every preset and format of objectKey: preset → format → URL.
func (s *Signer) URLs(objectKey string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(Presets()))
	for _, p := range Presets() {
		byFormat := make(map[string]string, len(Formats()))
		for _, f := range Formats() {
			byFormat[f] = s.URL(objectKey, p.Size, f)
		}
		out[p.Name] = byFormat
	}
	return out
}
