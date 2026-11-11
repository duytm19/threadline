package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/threadline/services/identity/internal/application/ports"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

const (
	maxUserAgentRunes = 512
	maxGeoHashRunes   = 128
	defaultGeoPepper  = "threadline-geo-v1"
	refreshTokenBytes = 32
	geohashBase32     = "0123456789bcdefghjkmnpqrstuvwxyz"
)

// NormalizeClientIP validates and canonicalizes a client IP address for audit logging.
// Handles host:port pairs, bracketed IPv6, IPv4-mapped IPv6, and rejects malformed/injected inputs.
func NormalizeClientIP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "unknown"
	}

	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "unknown"
		}
	}

	if idx := strings.IndexByte(trimmed, ','); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	if addr, err := netip.ParseAddr(trimmed); err == nil {
		return addr.Unmap().String()
	}

	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap().String()
		}
	}

	return "unknown"
}

// SanitizeUserAgent strips control characters (preventing log injection) and bounds length.
func SanitizeUserAgent(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "unknown"
	}

	var b strings.Builder
	count := 0
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			continue
		}
		if count >= maxUserAgentRunes {
			break
		}
		_, _ = b.WriteRune(r)
		count++
	}

	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

// SanitizeGeoHash validates an edge-supplied geographic hash string.
func SanitizeGeoHash(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxGeoHashRunes {
		return ""
	}
	for _, r := range trimmed {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !isAlphaNum && r != '-' && r != '_' && r != ':' {
			return ""
		}
	}
	return trimmed
}

// EncodeGeohash computes a standard base32 spatial geohash for valid WGS84 coordinates.
func EncodeGeohash(lat, lon float64, precision int) string {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return ""
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return ""
	}
	if precision <= 0 {
		precision = 5
	}
	if precision > 12 {
		precision = 12
	}

	latMin, latMax := -90.0, 90.0
	lonMin, lonMax := -180.0, 180.0

	var b strings.Builder
	b.Grow(precision)

	evenBit := true
	bit := 0
	ch := 0

	for b.Len() < precision {
		if evenBit {
			mid := (lonMin + lonMax) / 2
			if lon >= mid {
				ch = (ch << 1) | 1
				lonMin = mid
			} else {
				ch <<= 1
				lonMax = mid
			}
		} else {
			mid := (latMin + latMax) / 2
			if lat >= mid {
				ch = (ch << 1) | 1
				latMin = mid
			} else {
				ch <<= 1
				latMax = mid
			}
		}
		evenBit = !evenBit
		bit++
		if bit == 5 {
			_ = b.WriteByte(geohashBase32[ch])
			bit = 0
			ch = 0
		}
	}

	return b.String()
}

// DeterministicGeoHasher computes a privacy-preserving cryptographic geographic hash
// from edge geo hints or coarse IP CIDR prefixes (/24 for IPv4, /48 for IPv6).
type DeterministicGeoHasher struct {
	pepper string
}

// NewDeterministicGeoHasher constructs a DeterministicGeoHasher with the given domain-separation pepper.
func NewDeterministicGeoHasher(pepper string) *DeterministicGeoHasher {
	if strings.TrimSpace(pepper) == "" {
		pepper = defaultGeoPepper
	}
	return &DeterministicGeoHasher{pepper: pepper}
}

// ResolveGeoHash derives a deterministic privacy-preserving geographic hash.
func (g *DeterministicGeoHasher) ResolveGeoHash(_ context.Context, clientIP string, hint ports.GeoHint) string {
	if raw := SanitizeGeoHash(hint.RawGeo); raw != "" {
		return raw
	}

	if hint.Latitude != nil && hint.Longitude != nil {
		if cell := EncodeGeohash(*hint.Latitude, *hint.Longitude, 5); cell != "" {
			return hashWithPepper(g.pepper, "cell:"+cell)
		}
	}

	country := strings.ToUpper(strings.TrimSpace(hint.Country))
	region := strings.ToUpper(strings.TrimSpace(hint.Region))
	city := strings.ToUpper(strings.TrimSpace(hint.City))
	if country != "" || region != "" || city != "" {
		return hashWithPepper(g.pepper, fmt.Sprintf("loc:%s|%s|%s", country, region, city))
	}

	if addr, err := netip.ParseAddr(clientIP); err == nil {
		addr = addr.Unmap()
		prefixBits := 48
		if addr.Is4() {
			prefixBits = 24
		}
		if prefix, err := addr.Prefix(prefixBits); err == nil {
			return hashWithPepper(g.pepper, "cidr:"+prefix.Masked().String())
		}
	}

	return "unknown"
}

func hashWithPepper(pepper, payload string) string {
	sum := sha256.Sum256([]byte(pepper + "|" + payload))
	return hex.EncodeToString(sum[:])
}

// CryptoTokenGenerator generates CSPRNG refresh tokens and UUIDv7 token identifiers.
type CryptoTokenGenerator struct{}

// NewCryptoTokenGenerator constructs a production CSPRNG token generator.
func NewCryptoTokenGenerator() *CryptoTokenGenerator {
	return &CryptoTokenGenerator{}
}

// GenerateRefreshToken generates a 256-bit opaque URL-safe refresh token and its SHA-256 hex digest.
func (g *CryptoTokenGenerator) GenerateRefreshToken() (string, string, error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("application: failed to generate random refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return raw, tokenfamily.HashToken(raw), nil
}

// GenerateJTI generates a time-ordered UUIDv7 token identifier.
func (g *CryptoTokenGenerator) GenerateJTI() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString(), nil
	}
	return id.String(), nil
}

// SystemClock implements ports.Clock using UTC system time.
type SystemClock struct{}

// Now returns the current UTC timestamp.
func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}
