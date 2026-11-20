package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var indexPage = template.Must(template.New("index").Funcs(template.FuncMap{
	"bytes": FormatBytes,
	"score": func(v float64) string { return fmt.Sprintf("%.2f", v) },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>{{.Title}}</title>
<style>
:root { color-scheme: dark; --bg: #0d0e10; --surface: #15171a; --line: #2a2e34; --ink: #eef0f2; --ink-2: #b3b8c0; --ink-3: #7f858e; --a: #3987e5; --b: #d95926; }
@media (prefers-color-scheme: light) { :root { color-scheme: light; --bg: #eef0f2; --surface: #fbfbfc; --line: #dde0e4; --ink: #0e1013; --ink-2: #474c54; --ink-3: #7b818a; --a: #2a78d6; --b: #eb6834; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--ink); font: 14px/1.5 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 860px; margin: 0 auto; padding: 48px max(16px, env(safe-area-inset-right)) 64px max(16px, env(safe-area-inset-left)); }
h1 { font-size: 22px; font-weight: 600; margin: 0 0 24px; }
ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 1px; background: var(--line); border: 1px solid var(--line); border-radius: 10px; overflow: hidden; }
a { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 24px; padding: 16px 18px; background: var(--surface); color: inherit; text-decoration: none; }
a:hover { background: color-mix(in srgb, var(--surface) 92%, var(--ink)); }
a:focus-visible { outline: 2px solid var(--ink); outline-offset: -2px; }
h2 { font-size: 16px; font-weight: 600; margin: 0; }
p { margin: 0; color: var(--ink-3); font-size: 13px; }
.size { color: var(--ink-3); font: 12.5px ui-monospace, "SF Mono", Menlo, Consolas, monospace; text-align: right; }
dl { grid-column: 1 / -1; margin: 6px 0 0; display: flex; flex-wrap: wrap; gap: 4px 20px; font-size: 13px; color: var(--ink-2); }
dl div { display: flex; align-items: center; gap: 7px; }
dt::before { content: ""; display: inline-block; width: 8px; height: 8px; border-radius: 2px; margin-right: 7px; background: var(--ink-3); }
div:nth-child(1) > dt::before { background: var(--a); }
div:nth-child(2) > dt::before { background: var(--b); }
dd { margin: 0; font: 12.5px ui-monospace, "SF Mono", Menlo, Consolas, monospace; color: var(--ink); }
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<ul>
{{range .Items}}<li><a href="{{.Dir}}/">
  <h2>{{.M.Title}}{{if .Variant}} &middot; {{.Variant}}{{end}}</h2>
  <span class="size">{{bytes .Bytes}}</span>
  {{if .M.Subtitle}}<p>{{.M.Subtitle}}</p>{{end}}
  <dl>{{$label := .M.Metric.Label}}{{range .M.Encodes}}<div><dt>{{.Label}}</dt><dd>{{bytes .Bytes}}{{if .Score}}, {{$label}} {{score .Score.Mean}}{{end}}</dd></div>{{end}}</dl>
</a></li>
{{end}}</ul>
</main>
</body>
</html>
`))

// WriteIndex writes dir/index.html over every export one level down, and
// returns how many it listed and the size of everything under dir.
func WriteIndex(dir, title string) (count int, total int64, err error) {
	type item struct {
		Dir     string
		M       *Manifest
		Variant string
		Bytes   int64
	}
	found, err := filepath.Glob(filepath.Join(dir, "*", manifestName))
	if err != nil {
		return 0, 0, err
	}
	sort.Strings(found)
	var items []item
	for _, path := range found {
		body, err := os.ReadFile(path)
		if err != nil {
			return 0, 0, err
		}
		m := &Manifest{}
		if err := json.Unmarshal(body, m); err != nil {
			return 0, 0, fmt.Errorf("%s: %w", path, err)
		}
		it := item{Dir: filepath.Base(filepath.Dir(path)), M: m}
		for _, v := range m.Variants {
			if v.Href == "" {
				it.Variant = v.Label
			}
		}
		if it.Bytes, err = DirSize(filepath.Dir(path)); err != nil {
			return 0, 0, err
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return 0, 0, fmt.Errorf("no exports found under %s", dir)
	}

	out, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return 0, 0, err
	}
	err = indexPage.Execute(out, map[string]any{"Title": title, "Items": items})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to write index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nojekyll"), nil, 0o644); err != nil {
		return 0, 0, err
	}
	total, err = DirSize(dir)
	return len(items), total, err
}

// Preview serves an exported folder on localhost the way a static host would,
// range requests included.
func Preview(ctx context.Context, dir string, port int, open bool) error {
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return fmt.Errorf("%s has no index.html; is it an export?", dir)
	}
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		listener, err = lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to start server: %w", err)
		}
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return errors.New("failed to start server: no TCP address")
	}
	url := fmt.Sprintf("http://localhost:%d", addr.Port)
	fmt.Printf("Serving %s at %s\n\nPress Ctrl+C to stop\n", dir, url)
	if open {
		openBrowser(ctx, url)
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(dir)), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
