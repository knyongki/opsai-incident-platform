package analysis

import (
	"strings"
	"unicode/utf8"
)

// Sources of a stored result. Each one must also be allowed by the database.
const (
	SourceFake   = "fake"
	SourceClaude = "claude"
	SourceOpenAI = "openai"
)

// Provenance is trusted metadata about what produced a result. The application
// assigns it; it is never read from model output. Fake results carry no model or
// prompt version, and every real analyzer must carry both.
type Provenance struct {
	Source        string
	ModelName     string
	PromptVersion string
}

func (p Provenance) Validate() error {
	switch p.Source {
	case SourceFake:
		if p.ModelName == "" && p.PromptVersion == "" {
			return nil
		}
	case SourceClaude, SourceOpenAI:
		if validLabel(p.ModelName) && validLabel(p.PromptVersion) {
			return nil
		}
	}
	return ErrInvalid
}

func validLabel(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 200 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
