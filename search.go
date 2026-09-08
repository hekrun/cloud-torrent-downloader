package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var tagPattern = regexp.MustCompile(`(?s)<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
var nyaaRowPattern = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
var nyaaLinkPattern = regexp.MustCompile(`<a[^>]+href="(/view/[^"]+)"[^>]*>(.*?)</a>`)
var magnetPattern = regexp.MustCompile(`href="(magnet:\?[^\"]+)"`)
var nyaaCellPattern = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)

func search(ctx context.Context, provider, query string) ([]searchResult, error) {
	switch provider {
	case "piratebay":
		return searchPirateBay(ctx, query)
	case "nyaa":
		return searchNyaa(ctx, query)
	case "archive":
		return searchArchive(ctx, query)
	case "librivox":
		return searchLibriVox(ctx, query)
	case "all":
		pirate, pirateErr := searchPirateBay(ctx, query)
		nyaa, nyaaErr := searchNyaa(ctx, query)
		archive, archiveErr := searchArchive(ctx, query)
		librivox, librivoxErr := searchLibriVox(ctx, query)
		if pirateErr != nil && nyaaErr != nil && archiveErr != nil && librivoxErr != nil {
			return nil, fmt.Errorf("providers unavailable: pirate bay: %v; nyaa: %v; internet archive: %v; librivox: %v", pirateErr, nyaaErr, archiveErr, librivoxErr)
		}
		results := append(pirate, nyaa...)
		results = append(results, archive...)
		return append(results, librivox...), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}

func searchPirateBay(ctx context.Context, query string) ([]searchResult, error) {
	requestURL := "https://apibay.org/q.php?q=" + url.QueryEscape(query)
	body, err := fetch(ctx, requestURL)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		InfoHash string `json:"info_hash"`
		Size     string `json:"size"`
		Seeders  string `json:"seeders"`
		Leechers string `json:"leechers"`
		Added    string `json:"added"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("pirate bay response: %w", err)
	}
	results := make([]searchResult, 0, len(rows))
	for _, row := range rows {
		if row.Name == "" || row.InfoHash == "" {
			continue
		}
		link := "https://thepiratebay.org/search.php?q=" + url.QueryEscape(row.Name)
		if row.ID != "" {
			link = "https://thepiratebay.org/description.php?id=" + url.QueryEscape(row.ID)
		}
		results = append(results, searchResult{Title: html.UnescapeString(row.Name), Size: row.Size, Seeds: parseInt(row.Seeders), Peers: parseInt(row.Leechers), Date: row.Added, Magnet: magnetFromHash(row.InfoHash, row.Name), Link: link, Source: "Pirate Bay"})
		if len(results) == 40 {
			break
		}
	}
	return results, nil
}

func searchNyaa(ctx context.Context, query string) ([]searchResult, error) {
	requestURL := "https://nyaa.si/?f=0&c=0_0&q=" + url.QueryEscape(query)
	body, err := fetch(ctx, requestURL)
	if err != nil {
		return nil, err
	}
	results := make([]searchResult, 0, 40)
	for _, row := range nyaaRowPattern.FindAllStringSubmatch(string(body), -1) {
		match := nyaaLinkPattern.FindStringSubmatch(row[1])
		if len(match) != 3 {
			continue
		}
		title := cleanTitle(stripTags(html.UnescapeString(match[2])))
		if title == "" {
			continue
		}
		magnet := ""
		if magnetMatch := magnetPattern.FindStringSubmatch(row[1]); len(magnetMatch) == 2 {
			magnet = html.UnescapeString(magnetMatch[1])
		}
		results = append(results, searchResult{Title: title, Size: nyaaSize(row[1]), Magnet: magnet, Link: "https://nyaa.si" + match[1], Source: "Nyaa"})
		if len(results) == 40 {
			break
		}
	}
	return results, nil
}

func searchArchive(ctx context.Context, query string) ([]searchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Add("fl[]", "identifier")
	params.Add("fl[]", "title")
	params.Add("fl[]", "description")
	params.Set("rows", "40")
	params.Set("page", "1")
	params.Set("output", "json")
	body, err := fetch(ctx, "https://archive.org/advancedsearch.php?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var response struct {
		Response struct {
			Docs []struct {
				Identifier string `json:"identifier"`
				Title      string `json:"title"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("internet archive response: %w", err)
	}
	results := make([]searchResult, 0, len(response.Response.Docs))
	for _, item := range response.Response.Docs {
		if item.Identifier == "" || item.Title == "" {
			continue
		}
		results = append(results, searchResult{Title: html.UnescapeString(item.Title), Size: "Archive item", Link: "https://archive.org/details/" + url.PathEscape(item.Identifier), Source: "Internet Archive"})
	}
	return results, nil
}

func searchLibriVox(ctx context.Context, query string) ([]searchResult, error) {
	requestURL := "https://librivox.org/api/feed/audiobooks?search=" + url.QueryEscape(query) + "&format=json&limit=40"
	body, err := fetch(ctx, requestURL)
	if err != nil {
		return nil, err
	}
	var response struct {
		Books []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Authors []struct {
				FirstName string `json:"first_name"`
				LastName  string `json:"last_name"`
			} `json:"authors"`
			Duration string `json:"totaltimes"`
		} `json:"books"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("librivox response: %w", err)
	}
	results := make([]searchResult, 0, len(response.Books))
	for _, book := range response.Books {
		if book.ID == "" || book.Title == "" {
			continue
		}
		title := html.UnescapeString(book.Title)
		if len(book.Authors) > 0 {
			author := strings.TrimSpace(book.Authors[0].FirstName + " " + book.Authors[0].LastName)
			if author != "" {
				title += " - " + author
			}
		}
		results = append(results, searchResult{Title: title, Size: book.Duration, Link: "https://librivox.org/" + book.ID, Source: "LibriVox"})
	}
	return results, nil
}

func fetch(ctx context.Context, requestURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CloudTorrent/1.0")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned %s", response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

func stripTags(value string) string {
	return strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, ""))
}

func magnetFromHash(hash, title string) string {
	return fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", hash, url.QueryEscape(title))
}

func parseInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func cleanTitle(value string) string { return strings.TrimSpace(strings.ReplaceAll(value, "\n", " ")) }

func nyaaSize(row string) string {
	for _, cell := range nyaaCellPattern.FindAllStringSubmatch(row, -1) {
		value := cleanTitle(stripTags(html.UnescapeString(cell[1])))
		if strings.ContainsAny(value, "KMGT") && (strings.Contains(value, "B") || strings.Contains(value, "iB")) {
			return value
		}
	}
	return "Size unknown"
}
