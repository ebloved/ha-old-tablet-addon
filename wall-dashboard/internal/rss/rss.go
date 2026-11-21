package rss

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Item struct {
	Title     string `json:"title"`
	Link      string `json:"link"`
	Published string `json:"published"`
	Summary   string `json:"summary"`
	Content   string `json:"content,omitempty"`
	Source    string `json:"source"`
	Image     string `json:"image,omitempty"`
}

type cached struct {
	items []Item
	at    time.Time
}

type Cache struct {
	ttl    time.Duration
	log    *slog.Logger
	client *http.Client

	mu sync.Mutex
	m  map[string]cached
}

var tagRe = regexp.MustCompile(`<[^>]*>`)
var spaceRe = regexp.MustCompile(`\s+`)

func New(ttl time.Duration, log *slog.Logger) *Cache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Cache{
		ttl:    ttl,
		log:    log,
		client: &http.Client{Timeout: 15 * time.Second},
		m:      make(map[string]cached),
	}
}

func (c *Cache) Get(ctx context.Context, url string, limit int) ([]Item, error) {
	if limit <= 0 {
		limit = 8
	}
	c.mu.Lock()
	if e, ok := c.m[url]; ok && time.Since(e.at) < c.ttl {
		items := e.items
		c.mu.Unlock()
		return capItems(items, limit), nil
	}
	c.mu.Unlock()

	items, err := c.fetch(ctx, url, limit)
	if err != nil {
		c.mu.Lock()
		if e, ok := c.m[url]; ok {
			stale := e.items
			c.mu.Unlock()
			c.log.Warn("rss refresh failed, serving stale", "url", url, "err", err)
			return capItems(stale, limit), nil
		}
		c.mu.Unlock()
		return nil, err
	}

	c.mu.Lock()
	c.m[url] = cached{items: items, at: time.Now()}
	c.mu.Unlock()
	return capItems(items, limit), nil
}

func capItems(items []Item, limit int) []Item {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func (c *Cache) fetch(ctx context.Context, url string, limit int) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "old-tablet-dashboard/0.1")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	items, title, err := parse(body)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Source == "" {
			items[i].Source = title
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func parse(body []byte) ([]Item, string, error) {
	root, err := rootName(body)
	if err != nil {
		return nil, "", err
	}

	switch root {
	case "rss":
		var doc struct {
			Channel struct {
				Title string `xml:"title"`
				Items []struct {
					Title       string `xml:"title"`
					Link        string `xml:"link"`
					PubDate     string `xml:"pubDate"`
					Description string `xml:"description"`
					Enclosure   *struct {
						URL  string `xml:"url,attr"`
						Type string `xml:"type,attr"`
					} `xml:"enclosure"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		if err := xml.Unmarshal(body, &doc); err != nil {
			return nil, "", err
		}
		out := make([]Item, 0, len(doc.Channel.Items))
		for _, it := range doc.Channel.Items {
			enc := ""
			if it.Enclosure != nil {
				enc = it.Enclosure.URL
			}
			out = append(out, Item{
				Title:     clean(it.Title),
				Link:      strings.TrimSpace(it.Link),
				Published: normTime(it.PubDate),
				Summary:   summary(it.Description),
				Content:   longText(it.Description),
				Image:     pickImage(enc, it.Description),
			})
		}
		return out, clean(doc.Channel.Title), nil

	case "feed":
		var doc struct {
			Title   string `xml:"title"`
			Entries []struct {
				Title string `xml:"title"`
				Links []struct {
					Href string `xml:"href,attr"`
					Rel  string `xml:"rel,attr"`
				} `xml:"link"`
				Updated   string `xml:"updated"`
				Published string `xml:"published"`
				Summary   string `xml:"summary"`
				Content   string `xml:"content"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(body, &doc); err != nil {
			return nil, "", err
		}
		out := make([]Item, 0, len(doc.Entries))
		for _, e := range doc.Entries {
			link := ""
			enclosure := ""
			for _, l := range e.Links {
				if l.Rel == "enclosure" {
					enclosure = l.Href
				}
				if (l.Rel == "" || l.Rel == "alternate") && link == "" {
					link = l.Href
				}
			}
			bodyText := e.Summary
			if bodyText == "" {
				bodyText = e.Content
			}
			ts := e.Published
			if ts == "" {
				ts = e.Updated
			}
			out = append(out, Item{
				Title:     clean(e.Title),
				Link:      strings.TrimSpace(link),
				Published: normTime(ts),
				Summary:   summary(bodyText),
				Content:   longText(bodyText),
				Image:     pickImage(enclosure, bodyText),
			})
		}
		return out, clean(doc.Title), nil
	}

	return nil, "", fmt.Errorf("unsupported feed format: %s", root)
}

var imgRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)
var brRe = regexp.MustCompile(`(?i)<br\s*/?>`)
var pEndRe = regexp.MustCompile(`(?i)</p\s*>`)
var manyNL = regexp.MustCompile(`\n{3,}`)

func longText(s string) string {
	s = html.UnescapeString(s)
	s = brRe.ReplaceAllString(s, "\n")
	s = pEndRe.ReplaceAllString(s, "\n\n")
	s = tagRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	s = manyNL.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 4000 {
		r := []rune(s)
		s = string(r[:4000]) + "…"
	}
	return s
}

func pickImage(enclosure, body string) string {
	if u := strings.TrimSpace(enclosure); u != "" && safeImage(u) {
		return u
	}
	if m := imgRe.FindStringSubmatch(body); len(m) == 2 {
		u := html.UnescapeString(strings.TrimSpace(m[1]))
		if safeImage(u) {
			return u
		}
	}
	return ""
}

func safeImage(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func rootName(body []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("not a valid feed: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local, nil
		}
	}
}

func clean(s string) string {
	s = html.UnescapeString(s)
	s = tagRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func summary(s string) string {
	s = clean(s)
	if utf8.RuneCountInString(s) > 280 {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:280])) + "…"
	}
	return s
}

var timeLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func normTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}
