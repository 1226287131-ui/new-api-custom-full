package constant

import "strings"

const (
	MiniMaxH3Resolution480P  = "480p"
	MiniMaxH3Resolution768P  = "768p"
	MiniMaxH3Resolution1080P = "1080p"
)

// miniMaxH3ResolutionTierBySize is the MiniMax H3 size table. Its pixel
// dimensions are provider-specific, so those entries take precedence over
// generic video-resolution classification.
var miniMaxH3ResolutionTierBySize = map[string]string{
	"864x480": MiniMaxH3Resolution480P,
	"480x864": MiniMaxH3Resolution480P,
	"640x640": MiniMaxH3Resolution480P,
	"544x800": MiniMaxH3Resolution480P,
	"800x544": MiniMaxH3Resolution480P,
	"576x736": MiniMaxH3Resolution480P,
	"736x576": MiniMaxH3Resolution480P,
	"992x416": MiniMaxH3Resolution480P,

	"1376x768":  MiniMaxH3Resolution768P,
	"768x1376":  MiniMaxH3Resolution768P,
	"1024x1024": MiniMaxH3Resolution768P,
	"832x1248":  MiniMaxH3Resolution768P,
	"1248x832":  MiniMaxH3Resolution768P,
	"896x1184":  MiniMaxH3Resolution768P,
	"1184x896":  MiniMaxH3Resolution768P,
	"1568x672":  MiniMaxH3Resolution768P,

	"1920x1088": MiniMaxH3Resolution1080P,
	"1088x1920": MiniMaxH3Resolution1080P,
	"1440x1440": MiniMaxH3Resolution1080P,
	"1184x1760": MiniMaxH3Resolution1080P,
	"1760x1184": MiniMaxH3Resolution1080P,
	"1248x1664": MiniMaxH3Resolution1080P,
	"1664x1248": MiniMaxH3Resolution1080P,
	"2208x960":  MiniMaxH3Resolution1080P,
}

// MiniMaxH3ResolutionTierForSize returns the configured pricing tier for an
// H3 pixel size. It intentionally accepts only the MiniMax H3 size table.
func MiniMaxH3ResolutionTierForSize(size string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(size, "×", "x")))
	normalized = strings.ReplaceAll(normalized, " ", "")
	tier, ok := miniMaxH3ResolutionTierBySize[normalized]
	return tier, ok
}
