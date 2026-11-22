package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
)

// MaxLogoBody is sixteen times the largest built-in logo's body, Claude Code's at 3,855 bytes.
const MaxLogoBody = 64 << 10

// Logo is an SVG body drawn in currentColor inside a square view box of side View. The zero Logo draws the generic mark.
type Logo struct {
	View float64
	Body string
}

// NewLogo refuses only a logo that would break attn: a view box that is zero, negative or not finite, or a body over MaxLogoBody.
func NewLogo(view float64, body string) (Logo, error) {
	if math.IsNaN(view) || math.IsInf(view, 0) || view <= 0 {
		return Logo{}, fmt.Errorf("a logo's view must be a positive number, not %v", view)
	}
	if len(body) > MaxLogoBody {
		return Logo{}, fmt.Errorf("a logo's body is %d bytes, over the %d KiB limit", len(body), MaxLogoBody>>10)
	}
	return Logo{View: view, Body: body}, nil
}

// Hash names the logo by its content, or is "" for the generic mark.
func (l Logo) Hash() string {
	if l == (Logo{}) {
		return ""
	}
	sum := sha256.Sum256([]byte(strconv.FormatFloat(l.View, 'g', -1, 64) + "\n" + l.Body))
	return hex.EncodeToString(sum[:16])
}
