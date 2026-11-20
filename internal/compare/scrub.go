package compare

import (
	"bytes"
	"fmt"
	"regexp"
)

// localPath matches an absolute path under a home, volume or temp root. The
// leading group keeps it from firing inside a URL.
var localPath = regexp.MustCompile(
	`(^|[\s"'=:(\[<>,;])/(?:Users|Volumes|home|root|private|tmp|var|mnt|opt)/(?:[^\s"'/:;,()<>\[\]]+/)*([^\s"'/:;,()<>\[\]]*)`)

// privateRoots must never reach a published manifest.
var privateRoots = [][]byte{[]byte("/Users/"), []byte("/Volumes/"), []byte("/home/")}

// ScrubPaths cuts every local absolute path in s down to its file name, so
// "/Users/me/build/ffmpeg -i /Volumes/T7/in.mov" reads "ffmpeg -i in.mov".
func ScrubPaths(s string) string {
	return localPath.ReplaceAllString(s, "$1$2")
}

// scrub rewrites every free-text field. The player is published, and the
// commands and notes people paste in carry their machine's paths.
func (m *Manifest) scrub() {
	fix := func(s *string) { *s = ScrubPaths(*s) }
	fix(&m.Title)
	fix(&m.Subtitle)
	fix(&m.Metric.Label)
	fix(&m.Metric.Model)
	streams := m.Encodes
	if m.Reference != nil {
		streams = append(streams[:len(streams):len(streams)], m.Reference)
	}
	for i := range m.Variants {
		fix(&m.Variants[i].Label)
	}
	for _, s := range streams {
		fix(&s.Label)
		fix(&s.Detail)
		fix(&s.Codec)
	}
	if n := m.Notes; n != nil {
		fix(&n.Heading)
		for i := range n.Body {
			fix(&n.Body[i])
		}
		for i := range n.Commands {
			fix(&n.Commands[i].Label)
			fix(&n.Commands[i].Text)
		}
		for i := range n.Links {
			fix(&n.Links[i].Label)
		}
	}
}

// checkPrivate is the backstop behind scrub: it refuses to publish a manifest
// that still names a local path.
func checkPrivate(body []byte) error {
	for _, root := range privateRoots {
		if i := bytes.Index(body, root); i >= 0 {
			end := min(len(body), i+60)
			return fmt.Errorf("manifest still holds a local path: %q", body[i:end])
		}
	}
	return nil
}
