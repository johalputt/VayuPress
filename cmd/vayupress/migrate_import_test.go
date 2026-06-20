package main

import "testing"

func TestParseGhostExport(t *testing.T) {
	raw := []byte(`{"db":[{"data":{
		"posts":[
			{"id":"1","title":"Hello","slug":"hello","html":"<p>hi</p>","status":"published","published_at":"2024-01-02T03:04:05.000Z"},
			{"id":"2","title":"Draft One","slug":"draft-one","html":"<p>wip</p>","status":"draft","created_at":"2024-02-02T00:00:00.000Z"}
		],
		"tags":[{"id":"t1","name":"news"},{"id":"t2","name":"go"}],
		"posts_tags":[{"post_id":"1","tag_id":"t1"},{"post_id":"1","tag_id":"t2"}]
	}}]}`)
	posts, err := parseGhostExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}
	if posts[0].Title != "Hello" || posts[0].Slug != "hello" {
		t.Errorf("post0 wrong: %+v", posts[0])
	}
	if len(posts[0].Tags) != 2 {
		t.Errorf("expected 2 tags on post0, got %v", posts[0].Tags)
	}
	if !posts[1].Draft {
		t.Error("post1 should be a draft")
	}
	if posts[0].Created.Year() != 2024 {
		t.Errorf("bad created time: %v", posts[0].Created)
	}
}

func TestParseWXR(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?>
<rss xmlns:wp="x" xmlns:content="y">
<channel>
  <item>
    <title>First Post</title>
    <wp:post_name>first-post</wp:post_name>
    <wp:post_type>post</wp:post_type>
    <wp:status>publish</wp:status>
    <wp:post_date_gmt>2023-05-06 07:08:09</wp:post_date_gmt>
    <content:encoded><![CDATA[<p>body</p>]]></content:encoded>
    <category domain="post_tag">golang</category>
    <category domain="category">tech</category>
  </item>
  <item>
    <title>A Page</title>
    <wp:post_type>page</wp:post_type>
    <wp:status>publish</wp:status>
  </item>
</channel>
</rss>`)
	posts, err := parseWXR(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post (page skipped), got %d", len(posts))
	}
	p := posts[0]
	if p.Title != "First Post" || p.Slug != "first-post" {
		t.Errorf("wrong post: %+v", p)
	}
	if len(p.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", p.Tags)
	}
	if p.Created.Year() != 2023 {
		t.Errorf("bad created time: %v", p.Created)
	}
}
