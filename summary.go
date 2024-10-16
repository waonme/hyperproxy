package main

import (
	"net/http"

	"github.com/labstack/echo"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type Summary struct {
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Thumbnail   string `json:"thumbnail,omitempty"`
}

type Meta struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type Link struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
}

func SummaryHandler(c echo.Context) error {

	url := c.QueryParam("url")

	useragent := "hyperproxy summery bot"

	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", useragent)
	resp, _ := client.Do(req)

	favicon := ""
	title := ""
	summary := Summary{}
	twitter_card := "summary"

	z := html.NewTokenizer(resp.Body)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			goto END_ANALYSIS
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if atom.Lookup(name) == atom.Meta {
				meta := Meta{}
				for hasAttr {
					key, val, more := z.TagAttr()

					if string(key) == "name" || string(key) == "property" {
						meta.Name = string(val)
					} else if string(key) == "content" {
						meta.Content = string(val)
					}

					if !more {
						break
					}
				}

				if meta.Name == "og:title" {
					summary.Title = meta.Content
				} else if meta.Name == "og:description" {
					summary.Description = meta.Content
				} else if meta.Name == "og:image" {
					summary.Thumbnail = meta.Content
				} else if meta.Name == "twitter:card" {
					twitter_card = meta.Content
				}

			} else if atom.Lookup(name) == atom.Link {
				link := Link{}
				for hasAttr {
					key, val, more := z.TagAttr()

					if string(key) == "rel" {
						link.Rel = string(val)
					} else if string(key) == "href" {
						link.Href = string(val)
					}

					if !more {
						break
					}
				}

				if link.Rel == "icon" {
					favicon = link.Href
				} else if link.Rel == "shortcut icon" {
					favicon = link.Href
				}

			} else if atom.Lookup(name) == atom.Title {
				tt = z.Next()
				if tt == html.TextToken {
					title = string(z.Text())
				}
			}
		}
	}

END_ANALYSIS:

	if twitter_card != "summary_large_image" {
		summary.Icon = summary.Thumbnail
		summary.Thumbnail = ""
	}

	if summary.Icon == "" {
		summary.Icon = favicon
	}

	if summary.Title == "" {
		summary.Title = title
	}

	return c.JSON(http.StatusOK, summary)
}

