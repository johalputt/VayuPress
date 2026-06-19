package exporter

const articleTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{.Title}}</title>
  <meta name="description" content="{{.Description}}">
  <style>body{font-family:system-ui,sans-serif;max-width:720px;margin:2rem auto;padding:0 1rem}img{max-width:100%}</style>
</head>
<body>
  <nav><a href="/">&#8592; Home</a></nav>
  <article>
    <h1>{{.Title}}</h1>
    <time>{{.Date}}</time>
    {{.Content}}
  </article>
</body>
</html>`

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{.SiteTitle}}</title>
  <style>body{font-family:system-ui,sans-serif;max-width:720px;margin:2rem auto;padding:0 1rem}a{text-decoration:none}h2{margin-bottom:.25rem}.meta{color:#666;font-size:.9rem}</style>
</head>
<body>
  <h1>{{.SiteTitle}}</h1>
  <ul style="list-style:none;padding:0">
    {{range .Articles}}
    <li style="margin-bottom:1.5rem">
      <h2><a href="/articles/{{.Slug}}/">{{.Title}}</a></h2>
      <p class="meta"><time>{{.Date}}</time>{{if .Tags}} &middot; {{.Tags}}{{end}}</p>
    </li>
    {{end}}
  </ul>
  {{if .Pagination}}
  <nav style="margin-top:2rem">
    {{if .Pagination.Prev}}<a href="{{.Pagination.Prev}}">&#8592; Previous</a> {{end}}
    Page {{.Pagination.Current}} of {{.Pagination.Total}}
    {{if .Pagination.Next}} <a href="{{.Pagination.Next}}">Next &#8594;</a>{{end}}
  </nav>
  {{end}}
</body>
</html>`

const sitemapTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>{{.BaseURL}}/</loc>
    <changefreq>daily</changefreq>
    <priority>1.0</priority>
  </url>
  {{range .Articles}}
  <url>
    <loc>{{$.BaseURL}}/articles/{{.Slug}}/</loc>
    <lastmod>{{.UpdatedAt}}</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.8</priority>
  </url>
  {{end}}
</urlset>`

const feedTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>{{.SiteTitle}}</title>
    <link>{{.BaseURL}}</link>
    <description>{{.SiteTitle}} RSS Feed</description>
    {{range .Articles}}
    <item>
      <title>{{.Title}}</title>
      <link>{{$.BaseURL}}/articles/{{.Slug}}/</link>
      <pubDate>{{.PubDate}}</pubDate>
      <description>{{.Description}}</description>
    </item>
    {{end}}
  </channel>
</rss>`

const robotsTemplate = `User-agent: *
Allow: /

Sitemap: {{.BaseURL}}/sitemap.xml
`
