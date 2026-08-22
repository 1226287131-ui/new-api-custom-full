package constant

import "strings"

const (
	MiniMaxH3Resolution768P  = "768p"
	MiniMaxH3Resolution1080P = "1080p"
)

// miniMaxH3ResolutionTierBySize is the official MiniMax H3 size table. The
// provider charges the sizes in the two lists as distinct 768p and 1080p
// tiers, so this must not be inferred from pixel area.
var miniMaxH3ResolutionTierBySize = map[string]string{
	"448x448": MiniMaxH3Resolution768P, "576x576": MiniMaxH3Resolution768P, "640x640": MiniMaxH3Resolution768P, "736x736": MiniMaxH3Resolution768P,
	"800x800": MiniMaxH3Resolution768P, "864x864": MiniMaxH3Resolution768P, "928x928": MiniMaxH3Resolution768P, "960x960": MiniMaxH3Resolution768P,
	"384x576": MiniMaxH3Resolution768P, "448x672": MiniMaxH3Resolution768P, "544x800": MiniMaxH3Resolution768P, "576x896": MiniMaxH3Resolution768P,
	"640x960": MiniMaxH3Resolution768P, "704x1056": MiniMaxH3Resolution768P, "736x1120": MiniMaxH3Resolution768P, "800x1184": MiniMaxH3Resolution768P,
	"576x384": MiniMaxH3Resolution768P, "672x448": MiniMaxH3Resolution768P, "800x544": MiniMaxH3Resolution768P, "896x576": MiniMaxH3Resolution768P,
	"960x640": MiniMaxH3Resolution768P, "1056x704": MiniMaxH3Resolution768P, "1120x736": MiniMaxH3Resolution768P, "1184x800": MiniMaxH3Resolution768P,
	"384x544": MiniMaxH3Resolution768P, "480x640": MiniMaxH3Resolution768P, "576x736": MiniMaxH3Resolution768P, "640x832": MiniMaxH3Resolution768P,
	"672x928": MiniMaxH3Resolution768P, "736x992": MiniMaxH3Resolution768P, "800x1056": MiniMaxH3Resolution768P, "832x1120": MiniMaxH3Resolution768P,
	"544x384": MiniMaxH3Resolution768P, "640x480": MiniMaxH3Resolution768P, "736x576": MiniMaxH3Resolution768P, "832x640": MiniMaxH3Resolution768P,
	"928x672": MiniMaxH3Resolution768P, "992x736": MiniMaxH3Resolution768P, "1056x800": MiniMaxH3Resolution768P, "1120x832": MiniMaxH3Resolution768P,
	"352x608": MiniMaxH3Resolution768P, "416x736": MiniMaxH3Resolution768P, "480x864": MiniMaxH3Resolution768P, "544x960": MiniMaxH3Resolution768P,
	"608x1056": MiniMaxH3Resolution768P, "640x1152": MiniMaxH3Resolution768P, "672x1216": MiniMaxH3Resolution768P, "736x1280": MiniMaxH3Resolution768P,
	"608x352": MiniMaxH3Resolution768P, "736x416": MiniMaxH3Resolution768P, "864x480": MiniMaxH3Resolution768P, "960x544": MiniMaxH3Resolution768P,
	"1056x608": MiniMaxH3Resolution768P, "1152x640": MiniMaxH3Resolution768P, "1216x672": MiniMaxH3Resolution768P, "1280x736": MiniMaxH3Resolution768P,
	"704x288": MiniMaxH3Resolution768P, "864x352": MiniMaxH3Resolution768P, "992x416": MiniMaxH3Resolution768P, "1120x480": MiniMaxH3Resolution768P,
	"1216x512": MiniMaxH3Resolution768P, "1312x576": MiniMaxH3Resolution768P, "1408x608": MiniMaxH3Resolution768P, "1472x640": MiniMaxH3Resolution768P,

	"1024x1024": MiniMaxH3Resolution1080P, "1120x1120": MiniMaxH3Resolution1080P, "1248x1248": MiniMaxH3Resolution1080P, "1376x1376": MiniMaxH3Resolution1080P,
	"1440x1440": MiniMaxH3Resolution1080P,
	"832x1248":  MiniMaxH3Resolution1080P, "928x1376": MiniMaxH3Resolution1080P, "1024x1536": MiniMaxH3Resolution1080P, "1120x1696": MiniMaxH3Resolution1080P,
	"1184x1760": MiniMaxH3Resolution1080P,
	"1248x832":  MiniMaxH3Resolution1080P, "1376x928": MiniMaxH3Resolution1080P, "1536x1024": MiniMaxH3Resolution1080P, "1696x1120": MiniMaxH3Resolution1080P,
	"1760x1184": MiniMaxH3Resolution1080P,
	"864x1184":  MiniMaxH3Resolution1080P, "896x1184": MiniMaxH3Resolution1080P, "960x1280": MiniMaxH3Resolution1080P, "1088x1440": MiniMaxH3Resolution1080P,
	"1184x1600": MiniMaxH3Resolution1080P, "1248x1664": MiniMaxH3Resolution1080P,
	"1184x864": MiniMaxH3Resolution1080P, "1184x896": MiniMaxH3Resolution1080P, "1280x960": MiniMaxH3Resolution1080P, "1440x1088": MiniMaxH3Resolution1080P,
	"1600x1184": MiniMaxH3Resolution1080P, "1664x1248": MiniMaxH3Resolution1080P,
	"768x1344": MiniMaxH3Resolution1080P, "768x1376": MiniMaxH3Resolution1080P, "832x1504": MiniMaxH3Resolution1080P, "928x1664": MiniMaxH3Resolution1080P,
	"1024x1824": MiniMaxH3Resolution1080P, "1088x1920": MiniMaxH3Resolution1080P,
	"1344x768": MiniMaxH3Resolution1080P, "1376x768": MiniMaxH3Resolution1080P, "1504x832": MiniMaxH3Resolution1080P, "1664x928": MiniMaxH3Resolution1080P,
	"1824x1024": MiniMaxH3Resolution1080P, "1920x1088": MiniMaxH3Resolution1080P,
	"1536x672": MiniMaxH3Resolution1080P, "1568x672": MiniMaxH3Resolution1080P, "1728x736": MiniMaxH3Resolution1080P, "1920x832": MiniMaxH3Resolution1080P,
	"2112x896": MiniMaxH3Resolution1080P, "2208x960": MiniMaxH3Resolution1080P,
}

// MiniMaxH3ResolutionTierForSize returns the documented pricing tier for an
// H3 pixel size. It intentionally accepts only the official size table.
func MiniMaxH3ResolutionTierForSize(size string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(size, "×", "x")))
	normalized = strings.ReplaceAll(normalized, " ", "")
	tier, ok := miniMaxH3ResolutionTierBySize[normalized]
	return tier, ok
}
