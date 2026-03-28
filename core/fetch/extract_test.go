package fetch

import (
	"reflect"
	"testing"
)

func TestContentExtractor_ExtractHTMLCapturesVisibleLinks(t *testing.T) {
	result, err := NewContentExtractor().Extract([]byte(`
		<html>
			<head>
				<title>Link Test</title>
				<script>const hidden = '<a href="/ignore-me">ignore</a>';</script>
			</head>
			<body>
				<a href="/docs/start">Start</a>
				<a HREF="https://example.com/report?x=1&amp;y=2#top">Report</a>
				<a href='mailto:test@example.com'>Email</a>
				<a href=/docs/start>Duplicate</a>
				<!-- <a href="/hidden-comment">comment</a> -->
			</body>
		</html>
	`), "text/html; charset=utf-8")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	want := []string{
		"/docs/start",
		"https://example.com/report?x=1&y=2#top",
		"mailto:test@example.com",
	}
	if !reflect.DeepEqual(result.Links, want) {
		t.Fatalf("Links = %#v, want %#v", result.Links, want)
	}
}
