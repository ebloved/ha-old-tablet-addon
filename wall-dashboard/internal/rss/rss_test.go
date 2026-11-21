package rss

import "testing"

func TestParseRSSEnclosure(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rss version="2.0"><channel><title>T</title>
  <item>
    <title>Meme</title>
    <link>https://t.me/x/1</link>
    <description><![CDATA[<p>caption</p>]]></description>
    <enclosure url="https://wsrv.nl/?url=pic.jpg" type="image/jpeg"/>
  </item>
</channel></rss>`)

	items, title, err := parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if title != "T" || len(items) != 1 {
		t.Fatalf("title=%q items=%d", title, len(items))
	}
	if items[0].Image != "https://wsrv.nl/?url=pic.jpg" {
		t.Fatalf("image = %q", items[0].Image)
	}
}

func TestParseImageInDescription(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rss version="2.0"><channel><title>T</title>
  <item>
    <title>Photo</title>
    <link>https://t.me/x/2</link>
    <description><![CDATA[<p><img src="https://cdn.example/pic.jpg" alt="" /></p>]]></description>
  </item>
</channel></rss>`)

	items, _, err := parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Image != "https://cdn.example/pic.jpg" {
		t.Fatalf("image = %q", items[0].Image)
	}
}

func TestParseAtomEnclosure(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>A</title>
  <entry>
    <title>Post</title>
    <link rel="alternate" href="https://t.me/x/3"/>
    <link rel="enclosure" href="https://cdn.example/pic2.jpg"/>
    <updated>2026-11-21T10:00:00Z</updated>
  </entry>
</feed>`)

	items, _, err := parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Image != "https://cdn.example/pic2.jpg" {
		t.Fatalf("image = %q", items[0].Image)
	}
}

func TestUnsafeImageRejected(t *testing.T) {
	body := []byte(`<rss version="2.0"><channel><title>T</title>
<item><title>X</title><enclosure url="javascript:alert(1)"/></item>
</channel></rss>`)
	items, _, err := parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Image != "" {
		t.Fatalf("unsafe image kept: %q", items[0].Image)
	}
}
