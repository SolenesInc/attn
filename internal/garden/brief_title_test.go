package garden

import (
	"strings"
	"testing"
)

func TestABriefTitleIsItsFirstHeadingOrFirstLine(t *testing.T) {
	longTitle := strings.Repeat("invoice ", 10) + "reconciliation more detail"
	for _, row := range []struct{ name, brief, want string }{
		{"heading", "# Fix the queue jump\n\nInvestigate queue movement.", "Fix the queue jump"},
		{"hash in the heading text", "# Document C#", "Document C#"},
		{"closing hashes after a text hash", "# Document C# ###", "Document C#"},
		{"later heading", "Context first\n\n### Reconcile  the\tledgers ###\nBody", "Reconcile the ledgers"},
		{"fenced example falls back to first line", "Fix the queue jump\n\n```markdown\n# Example\n```", "Fix the queue jump"},
		{"real heading after fenced example", "Context first\n\n```markdown\n# Example\n```\n\n# Fix the queue jump", "Fix the queue jump"},
		{"tilde fence", "Context first\n~~~markdown\n# Example\n~~~\n# Fix the queue jump", "Fix the queue jump"},
		{"shorter fence cannot close", "Context first\n````markdown\n```\n# Example\n````\n# Fix the queue jump", "Fix the queue jump"},
		{"different marker cannot close", "Context first\n~~~markdown\n```\n# Example\n~~~\n# Fix the queue jump", "Fix the queue jump"},
		{"closing fence must end the line", "Context first\n```markdown\n```still code\n# Example\n```\n# Fix the queue jump", "Fix the queue jump"},
		{"longer closing fence", "Context first\n```markdown\n# Example\n````` \t\n# Fix the queue jump", "Fix the queue jump"},
		{"indented fences and CRLF", "Context first\r\n   ```markdown\r\n# Example\r\n  ``` \t\r\n# Fix the queue jump", "Fix the queue jump"},
		{"unclosed fence", "Fix the queue jump\n```markdown\n# Example", "Fix the queue jump"},
		{"backticks in info do not open a fence", "Context first\n``` example `inline`\n# Fix the queue jump", "Fix the queue jump"},
		{"empty heading does not stop scanning", "Context first\n# ###\n# Fix the queue jump", "Fix the queue jump"},
		{"later indented line is not a title heading", "Context first\n   # Example\n# Fix the queue jump", "Fix the queue jump"},
		{"first non-empty line", "\n\t Reconcile  the ledgers \nDetails", "Reconcile the ledgers"},
		{"long first line cuts at a word", longTitle, strings.TrimSpace(strings.Repeat("invoice ", 10))},
		{"long unicode heading cuts at 80 runes", "###### " + strings.Repeat("é", 90), strings.Repeat("é", 80)},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := TitleFromBrief(row.brief); got != row.want {
				t.Errorf("TitleFromBrief = %q; want %q", got, row.want)
			}
		})
	}
}
