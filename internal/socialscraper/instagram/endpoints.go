package instagram

import (
	"net/http"
	"net/url"
	"strconv"
)

// Every Instagram endpoint path and request header lives here; the module contacts these two hosts only.
const (
	webHost    = "www.instagram.com"
	mobileHost = "i.instagram.com" // the web app calls it for feeds, media info and comments

	// webAppID is the public X-IG-App-ID of Instagram's own web app.
	webAppID  = "936619743392459"
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"
)

func endpoint(host, path string, q url.Values) string {
	u := url.URL{Scheme: "https", Host: host, Path: path, RawQuery: q.Encode()}
	return u.String()
}

func profileURL(username string) string {
	return endpoint(webHost, "/api/v1/users/web_profile_info/", url.Values{"username": {username}})
}

func feedURL(userID string, count int, maxID string) string {
	q := url.Values{"count": {strconv.Itoa(count)}}
	if maxID != "" {
		q.Set("max_id", maxID)
	}
	return endpoint(mobileHost, "/api/v1/feed/user/"+url.PathEscape(userID)+"/", q)
}

func mediaInfoURL(mediaID string) string {
	return endpoint(mobileHost, "/api/v1/media/"+mediaID+"/info/", nil)
}

// commentsURL: cursorKey is "min_id" or "max_id" (the key Instagram named the cursor with), cursor "" for the first page.
func commentsURL(mediaID, cursorKey, cursor string) string {
	q := url.Values{"can_support_threading": {"true"}, "permalink_enabled": {"false"}}
	if cursor != "" {
		q.Set(cursorKey, cursor)
	}
	return endpoint(mobileHost, "/api/v1/media/"+mediaID+"/comments/", q)
}

func childCommentsURL(mediaID, commentID string) string {
	return endpoint(mobileHost, "/api/v1/media/"+mediaID+"/comments/"+url.PathEscape(commentID)+"/child_comments/", nil)
}

// setHeaders adds the web headers and, with a session, its cookies.
func (m *Module) setHeaders(h http.Header) {
	h.Set("User-Agent", userAgent)
	h.Set("Accept", "*/*")
	h.Set("X-IG-App-ID", webAppID)
	if s := m.session; s != nil {
		h.Set("Cookie", s.cookie())
		if s.CSRFToken != "" {
			h.Set("X-CSRFToken", s.CSRFToken)
		}
	}
}
