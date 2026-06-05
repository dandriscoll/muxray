package diag

import (
	"bufio"
	"os"
	"strings"

	"github.com/dandriscoll/muxray/internal/telemetry"
)

// Bundle is a sanitized diagnostic bundle for bug reports. It contains the
// doctor report and recent local debug-log events. It contains NO pane content
// by default; an excerpt is included only when the caller explicitly opts in,
// and even then it is run through Redact.
type Bundle struct {
	Generated       string   `json:"generated_at"`
	Doctor          Report   `json:"doctor"`
	RecentEvents    []string `json:"recent_events"`
	ExcerptIncluded bool     `json:"excerpt_included"`
	Excerpt         string   `json:"excerpt,omitempty"`
	Note            string   `json:"note"`
}

// BuildBundle assembles a sanitized bundle. When includeExcerpt is true the
// provided excerpt is redacted and attached; otherwise no content is included.
func BuildBundle(muxrayVersion, generatedAt string, includeExcerpt bool, excerpt string) Bundle {
	b := Bundle{
		Generated:    generatedAt,
		Doctor:       Doctor(muxrayVersion),
		RecentEvents: recentEvents(50),
		Note:         "Sanitized bundle. Pane content is omitted by default; any included excerpt is secret-redacted (best-effort). Review before sharing.",
	}
	if includeExcerpt && excerpt != "" {
		b.ExcerptIncluded = true
		b.Excerpt = Redact(excerpt)
	}
	return b
}

// recentEvents returns up to n trailing lines of the local debug log (each line
// is an already-content-free telemetry Event). Missing log → empty.
func recentEvents(n int) []string {
	path := telemetry.DebugLogPath()
	if path == "" {
		return []string{}
	}
	f, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if lines == nil {
		return []string{}
	}
	return lines
}
